package jobmanager

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/jobs"
)

var _ manager.Manager = &JobManager{}

type JobManager struct {
	cache         manager.Cache
	db            manager.Database
	d             manager.Deployment
	apiGw         manager.ApiGw
	repo          manager.Repository
	notifs        manager.Notifs
	maxAnchorJobs int
	minAnchorJobs int
	paused        bool
	env           manager.EnvType
	waitGroup     *sync.WaitGroup
}

func NewJobManager(cache manager.Cache, db manager.Database, d manager.Deployment, apiGw manager.ApiGw, repo manager.Repository, notifs manager.Notifs) (manager.Manager, error) {
	maxAnchorJobs := manager.DefaultCasMaxAnchorWorkers
	if configMaxAnchorWorkers, found := os.LookupEnv("CAS_MAX_ANCHOR_WORKERS"); found {
		if parsedMaxAnchorWorkers, err := strconv.Atoi(configMaxAnchorWorkers); err == nil {
			maxAnchorJobs = parsedMaxAnchorWorkers
		}
	}
	minAnchorJobs := manager.DefaultCasMinAnchorWorkers
	if configMinAnchorWorkers, found := os.LookupEnv("CAS_MIN_ANCHOR_WORKERS"); found {
		if parsedMinAnchorWorkers, err := strconv.Atoi(configMinAnchorWorkers); err == nil {
			minAnchorJobs = parsedMinAnchorWorkers
		}
	}
	if minAnchorJobs > maxAnchorJobs {
		return nil, fmt.Errorf("newJobManager: invalid anchor worker config: %d, %d", minAnchorJobs, maxAnchorJobs)
	}
	paused, _ := strconv.ParseBool(os.Getenv("PAUSED"))
	return &JobManager{cache, db, d, apiGw, repo, notifs, maxAnchorJobs, minAnchorJobs, paused, manager.EnvType(os.Getenv("ENV")), new(sync.WaitGroup)}, nil
}

func (m *JobManager) NewJob(jobState manager.JobState) (string, error) {
	jobState.Stage = manager.JobStage_Queued
	// Only set the job ID/time if not already set by the caller
	if len(jobState.Id) == 0 {
		jobState.Id = uuid.New().String()
	}
	if jobState.Ts.IsZero() {
		jobState.Ts = time.Now()
	}
	if jobState.Params == nil {
		jobState.Params = make(map[string]interface{}, 0)
	}
	return jobState.Id, m.db.QueueJob(jobState)
}

func (m *JobManager) CheckJob(jobId string) string {
	if job, found := m.cache.JobById(jobId); found {
		return string(job.Stage)
	}
	return ""
}

func (m *JobManager) ProcessJobs(shutdownCh chan bool) {
	// Create a ticker to poll the database for new jobs
	tick := time.NewTicker(manager.DefaultTick)
	// Only allow one run token to exist, and start with it available for the processing loop to start running.
	runToken := make(chan bool, 1)
	runToken <- true
	for {
		log.Println("manager: start processing jobs...")
		for {
			select {
			case <-shutdownCh:
				log.Println("manager: stop processing jobs...")
				tick.Stop()
				// Attempt to acquire the run token to ensure that no jobs are being processed while shutting down
				<-runToken
				return
			case <-tick.C:
				// Acquire the run token so that no loop iterations can run in parallel (shouldn't happen), and so that
				// shutdown can't complete until a running iteration has finished.
				<-runToken
				m.processJobs()
				// Release the run token
				runToken <- true
			}
		}
	}
}

func (m *JobManager) Pause() {
	// Toggle paused status
	m.paused = !m.paused
	status := "paused"
	if !m.paused {
		status = "un" + status
	}
	log.Printf("pause: job manager %s", status)
}

func (m *JobManager) processJobs() {
	now := time.Now()
	// Age out completed/failed/skipped jobs older than 1 day
	oldJobs := m.cache.JobsByMatcher(func(js manager.JobState) bool {
		return manager.IsFinishedJob(js) && now.AddDate(0, 0, -manager.DefaultTtlDays).After(js.Ts)
	})
	if len(oldJobs) > 0 {
		log.Printf("processJobs: aging out %d jobs...", len(oldJobs))
		for _, job := range oldJobs {
			// Delete the job from the cache
			log.Printf("processJobs: aging out job: %s", manager.PrintJob(job))
			m.cache.DeleteJob(job.Id)
		}
	}
	// Find all jobs in progress and advance their state before looking for new jobs
	activeJobs := m.cache.JobsByMatcher(manager.IsActiveJob)
	if len(activeJobs) > 0 {
		log.Printf("processJobs: checking %d jobs in progress: %s", len(activeJobs), manager.PrintJob(activeJobs...))
		for _, job := range activeJobs {
			m.advanceJob(job)
		}
	}
	// Wait for any running job advancement goroutines to finish before kicking off more jobs
	m.waitGroup.Wait()
	// Schedule tests and check anchor job interval
	if err := m.scheduleTests(); err != nil {
		log.Printf("processJobs: error scheduling tests: %v", err)
	}
	// Don't start any new jobs if the job manager is paused. Existing jobs will continue to be advanced.
	if !m.paused {
		// Always attempt to check if we have anchor jobs, even if none were dequeued. This is because we might have a
		// configured minimum number of jobs to run.
		processAnchorJobs := true
		// Try to dequeue multiple jobs and collapse similar ones:
		// - one deploy at a time
		// - any number of anchor workers (compatible with with smoke/E2E tests)
		// - one smoke test at a time (compatible with anchor workers, E2E tests)
		// - one E2E test at a time (compatible with anchor workers, smoke tests)
		//
		// Loop over compatible dequeued jobs until we find an incompatible one and need to wait for existing jobs to
		// complete.
		dequeuedJobs := m.db.DequeueJobs()
		if len(dequeuedJobs) > 0 {
			log.Printf("processJobs: dequeued %d jobs...", len(dequeuedJobs))
			// Preprocess dequeued jobs, and filter successfully prepared ones.
			// Ref: https://github.com/golang/go/wiki/SliceTricks#filtering-without-allocating
			tempSlice := dequeuedJobs[:0]
			for _, jobState := range dequeuedJobs {
				if _, err := m.prepareJob(jobState); err != nil {
					log.Printf("processJobs: job generation failed: %v, %s", err, manager.PrintJob(jobState))
				} else {
					tempSlice = append(tempSlice, jobState)
				}
			}
			dequeuedJobs = tempSlice
			// Recheck the length of `dequeuedJobs` in case any job(s) failed preprocessing and got filtered out
			if len(dequeuedJobs) > 0 {
				// Check for any force deploy jobs, and only look at the remaining jobs if no deployments were kicked
				// off.
				if m.processForceDeployJobs(dequeuedJobs) {
					processAnchorJobs = false
				} else
				// Decide how to proceed based on the first job from the list
				if dequeuedJobs[0].Type == manager.JobType_Deploy {
					m.processDeployJobs(dequeuedJobs)
					// There are two scenarios for anchor jobs on encountering a deploy job at the head of the queue:
					// - Anchor jobs are started if no deployment was *started*, even if this deploy job was ahead of
					//   anchor jobs in the queue.
					// - Anchor jobs are not started since a deploy job was *dequeued* ahead of them. (This would be the
					//   normal behavior for a job queue, i.e. jobs get processed in the order they were scheduled.)
					//
					// The first scenario only applies to the QA environment that is used for running the E2E tests. E2E
					// tests need anchor jobs to run, but if all jobs are processed sequentially, anchor jobs required
					// for processing test streams can get blocked by deploy jobs, which are in turn blocked by the E2E
					// tests themselves. Letting anchor jobs "skip the queue" prevents this "deadlock".
					//
					// Testing for this scenario can be simplified by checking whether E2E tests were in progress. So,
					// anchor jobs will only be able to "skip the queue" if E2E tests were running but fallback to
					// sequential processing otherwise. Since E2E tests only run in QA, all other environments (and QA
					// for all other scenarios besides active E2E tests) will have the default (sequential) behavior.
					e2eTestJobs := m.cache.JobsByMatcher(func(js manager.JobState) bool {
						return manager.IsActiveJob(js) && (js.Type == manager.JobType_TestE2E)
					})
					processAnchorJobs = len(e2eTestJobs) > 0
				} else {
					m.processTestJobs(dequeuedJobs)
				}
			}
		}
		// If no deploy jobs were launched, process pending anchor jobs. We don't want to hold on to anchor jobs queued
		// behind deploys because tests need anchors to run, and deploys can't run till tests complete.
		//
		// Test and anchor jobs can run in parallel, so process pending anchor jobs even if tests were started above.
		if processAnchorJobs {
			m.processAnchorJobs(dequeuedJobs)
		}
	}
	// Wait for all of this iteration's job advancement goroutines to finish before we iterate again. The ticker will
	// automatically drop ticks then pick back up later if a round of processing takes longer than 1 tick.
	m.waitGroup.Wait()
}

func (m *JobManager) scheduleTests() error {
	scheduleTest := func(testType manager.JobType) error {
		newJob := manager.JobState{
			Ts:   time.Now(),
			Type: testType,
			Params: map[string]interface{}{
				manager.JobParam_Source: manager.ServiceName,
			},
		}
		if _, err := m.NewJob(newJob); err != nil {
			log.Printf("scheduleTest: failed to queue test: %v, %s, %s", err, testType, manager.PrintJob(newJob))
			return err
		}
		log.Printf("scheduleTest: scheduled test: %s", manager.PrintJob(newJob))
		return nil
	}
	if err := m.checkJobInterval(manager.JobType_TestSmoke, manager.JobStage_Queued, "SMOKE_TEST_INTERVAL", func(time.Time) error {
		return scheduleTest(manager.JobType_TestSmoke)
	}); err != nil {
		log.Printf("scheduleTests: failed to queue smoke tests: %v", err)
		return err
	} else if m.env == manager.EnvType_Qa { // Only schedule E2E tests in QA
		if err = m.checkJobInterval(manager.JobType_TestE2E, manager.JobStage_Queued, "E2E_TEST_INTERVAL", func(time.Time) error {
			return scheduleTest(manager.JobType_TestE2E)
		}); err != nil {
			log.Printf("scheduleTests: failed to queue e2e tests: %v", err)
			return err
		}
	}
	return nil
}

func (m *JobManager) checkJobInterval(jobType manager.JobType, jobStage manager.JobStage, intervalEnv string, processFn func(time.Time) error) error {
	if interval, found := os.LookupEnv(intervalEnv); found {
		if parsedInterval, err := time.ParseDuration(interval); err != nil {
			log.Printf("checkJobInterval: failed to parse interval: %s, %s, %v", jobType, intervalEnv, err)
			return err
		} else {
			now := time.Now()
			var lastJob *manager.JobState = nil
			// Iterate the DB in descending order of timestamp
			if err = m.db.IterateByType(jobType, false, func(js manager.JobState) bool {
				if js.Stage == jobStage {
					lastJob = &js
					// Stop iterating, we found the job we were looking for.
					return false
				}
				// Keep iterating till we find the most recent job
				return true
			}); err != nil {
				log.Printf("checkJobInterval: error iterating over %s: %v", jobType, err)
				return err
			}
			// Only call `processFn` if we found an appropriate job
			if (lastJob != nil) && now.Add(-parsedInterval).After(lastJob.Ts) {
				return processFn(lastJob.Ts)
			}
		}
	}
	return nil
}

func (m *JobManager) processForceDeployJobs(dequeuedJobs []manager.JobState) bool {
	// Collapse all force deploys for the same component
	forceDeploys := make(map[string]manager.JobState, 0)
	for _, dequeuedJob := range dequeuedJobs {
		if dequeuedJob.Type == manager.JobType_Deploy {
			if dequeuedJobForce, _ := dequeuedJob.Params[manager.JobParam_Force].(bool); dequeuedJobForce {
				// Replace an existing job with a newer one, or add a new job (hence a map).
				forceDeploys[dequeuedJob.Params[manager.JobParam_Component].(string)] = dequeuedJob
			}
		}
	}
	if len(forceDeploys) > 0 {
		// Skip any dequeued jobs for components being force deployed
		for _, dequeuedJob := range dequeuedJobs {
			if dequeuedJob.Type == manager.JobType_Deploy {
				if forceDeploy, found := forceDeploys[dequeuedJob.Params[manager.JobParam_Component].(string)]; found && (dequeuedJob.Id != forceDeploy.Id) {
					if err := m.updateJobStage(dequeuedJob, manager.JobStage_Skipped); err != nil {
						// Return `true` from here so that no state is changed and the loop can restart cleanly. Any
						// jobs already skipped won't be picked up again, which is ok.
						return true
					}
				}
			}
		}
		// Cancel any running jobs for components being force deployed
		activeDeploys := m.cache.JobsByMatcher(func(js manager.JobState) bool {
			return manager.IsActiveJob(js) && (js.Type == manager.JobType_Deploy)
		})
		for _, activeDeploy := range activeDeploys {
			if _, found := forceDeploys[activeDeploy.Params[manager.JobParam_Component].(string)]; found {
				if err := m.updateJobStage(activeDeploy, manager.JobStage_Canceled); err != nil {
					// Return `true` from here so that no state is changed and the loop can restart cleanly. Any jobs
					// already skipped won't be picked up again, which is ok.
					return true
				}
			}
		}
		// Now advance all force deploy jobs, order doesn't matter.
		for _, deployJob := range forceDeploys {
			log.Printf("processForceDeployJobs: starting force deploy job: %s", manager.PrintJob(deployJob))
			m.advanceJob(deployJob)
		}
		return true
	}
	return false
}

func (m *JobManager) processDeployJobs(dequeuedJobs []manager.JobState) bool {
	// Check if there are any jobs in progress
	activeJobs := m.cache.JobsByMatcher(manager.IsActiveJob)
	if len(activeJobs) == 0 {
		// We know the first job is a deploy, so pick out the component for that job, collapse as many back-to-back jobs
		// as possible for that component, then run the final job.
		deployJob := dequeuedJobs[0]
		deployComponent := deployJob.Params[manager.JobParam_Component].(string)
		// Collapse similar, back-to-back deployments into a single run and kick it off.
		for i := 1; i < len(dequeuedJobs); i++ {
			dequeuedJob := dequeuedJobs[i]
			// Break out of the loop as soon as we find a test job - we don't want to collapse deploys across them.
			if (dequeuedJob.Type == manager.JobType_TestE2E) || (dequeuedJob.Type == manager.JobType_TestSmoke) {
				break
			} else if (dequeuedJob.Type == manager.JobType_Deploy) && (dequeuedJob.Params[manager.JobParam_Component].(string) == deployComponent) {
				// Skip the current deploy job, and replace it with a newer one.
				if err := m.updateJobStage(deployJob, manager.JobStage_Skipped); err != nil {
					// Return `true` from here so that no state is changed and the loop can restart cleanly. Any
					// jobs already skipped won't be picked up again, which is ok.
					return true
				}
				deployJob = dequeuedJob
			}
		}
		log.Printf("processDeployJobs: starting deploy job: %s", manager.PrintJob(deployJob))
		m.advanceJob(deployJob)
		return true
	} else {
		log.Printf("processDeployJobs: other jobs in progress")
	}
	return false
}

func (m *JobManager) processAnchorJobs(dequeuedJobs []manager.JobState) bool {
	// Check if there are any deploy jobs in progress
	activeDeploys := m.cache.JobsByMatcher(func(js manager.JobState) bool {
		return manager.IsActiveJob(js) && (js.Type == manager.JobType_Deploy)
	})
	if len(activeDeploys) == 0 {
		return m.processVxAnchorJobs(dequeuedJobs, true) || m.processVxAnchorJobs(dequeuedJobs, false)
	} else {
		log.Printf("processAnchorJobs: deployment in progress")
	}
	return false
}

func (m *JobManager) processVxAnchorJobs(dequeuedJobs []manager.JobState, processV5Jobs bool) bool {
	// Lookup any anchor jobs in progress
	activeAnchors := m.cache.JobsByMatcher(func(js manager.JobState) bool {
		// Returns true if `processV5Jobs=true` and this is a v5 worker job, or if `processV5Jobs=false` and this is a
		// v2 worker job.
		return manager.IsActiveJob(js) && (js.Type == manager.JobType_Anchor) && (processV5Jobs == manager.IsV5WorkerJob(js))
	})
	dequeuedAnchors := make([]manager.JobState, 0, 0)
	for _, dequeuedJob := range dequeuedJobs {
		if (dequeuedJob.Type == manager.JobType_Anchor) && (processV5Jobs == manager.IsV5WorkerJob(dequeuedJob)) {
			// Launch a new anchor job if:
			//  - This is a v5 job - the v5 Scheduler is responsible for scaling/capping v5 workers
			//  - The maximum number of anchor jobs is -1 (infinity)
			//  - The number of active anchor jobs + the number of dequeued jobs < the configured maximum
			if manager.IsV5WorkerJob(dequeuedJob) ||
				(m.maxAnchorJobs == -1) ||
				(len(activeAnchors)+len(dequeuedAnchors) < m.maxAnchorJobs) {
				dequeuedAnchors = append(dequeuedAnchors, dequeuedJob)
			} else
			// Skip any pending anchor jobs so that they don't linger in the job queue
			if err := m.updateJobStage(dequeuedJob, manager.JobStage_Skipped); err != nil {
				// Return `true` from here so that no state is changed and the loop can restart cleanly. Any jobs
				// already skipped won't be picked up again, which is ok.
				return true
			}
		}
	}
	// Now advance all anchor jobs, order doesn't matter.
	for _, anchorJob := range dequeuedAnchors {
		log.Printf("processVxAnchorJobs: starting anchor job: %s", manager.PrintJob(anchorJob))
		m.advanceJob(anchorJob)
	}
	// If not enough anchor jobs were running to satisfy the configured minimum number of workers, add jobs to the queue
	// to make up the difference. These jobs should get picked up in a subsequent job manager iteration, properly
	// coordinated with other jobs in the queue. It's ok if we ultimately end up with more jobs queued than the
	// configured maximum number of workers - the actual number of jobs run will be capped correctly.
	//
	// This mode is enforced via configuration to only be enabled when the scheduler is not running so that there is a
	// single source for new anchor jobs.
	//
	// This mode will not be used for v5 anchor jobs - the v5 Scheduler is responsible for scaling v5 workers.
	numJobs := len(dequeuedAnchors)
	if !processV5Jobs {
		for i := 0; i < m.minAnchorJobs-numJobs; i++ {
			if _, err := m.NewJob(manager.JobState{
				Type: manager.JobType_Anchor,
				Params: map[string]interface{}{
					manager.JobParam_Source: manager.ServiceName,
				},
			}); err != nil {
				log.Printf("processVxAnchorJobs: failed to queue additional anchor job: %v", err)
			}
		}
	}
	return numJobs > 0
}

func (m *JobManager) processTestJobs(dequeuedJobs []manager.JobState) bool {
	// Check if there are any deploy jobs in progress
	activeDeploys := m.cache.JobsByMatcher(func(js manager.JobState) bool {
		return manager.IsActiveJob(js) && (js.Type == manager.JobType_Deploy)
	})
	if len(activeDeploys) == 0 {
		// - Collapse all smoke tests between deployments into a single run
		// - Collapse all E2E tests between deployments into a single run
		dequeuedTests := make(map[manager.JobType]manager.JobState, 0)
		for _, dequeuedJob := range dequeuedJobs {
			// Break out of the loop as soon as we find a deploy job. We don't want to collapse test jobs across deploy
			// jobs.
			if dequeuedJob.Type == manager.JobType_Deploy {
				break
			} else if (dequeuedJob.Type == manager.JobType_TestE2E) || (dequeuedJob.Type == manager.JobType_TestSmoke) {
				// Update the cache and database for every skipped job
				if jobToSkip, found := dequeuedTests[dequeuedJob.Type]; found {
					if err := m.updateJobStage(jobToSkip, manager.JobStage_Skipped); err != nil {
						// Return `true` from here so that no state is changed and the loop can restart cleanly. Any
						// jobs already skipped won't be picked up again, which is ok.
						return true
					}
				}
				// Replace an existing test job with a newer one, or add a new job (hence a map).
				dequeuedTests[dequeuedJob.Type] = dequeuedJob
			}
		}
		for _, testJob := range dequeuedTests {
			log.Printf("processTestJobs: starting test job: %s", manager.PrintJob(testJob))
			m.advanceJob(testJob)
		}
		return len(dequeuedTests) > 0
	} else {
		log.Printf("processTestJobs: deployment in progress")
	}
	return false
}

func (m *JobManager) advanceJob(jobState manager.JobState) {
	m.waitGroup.Add(1)
	go func() {
		defer func() {
			m.waitGroup.Done()
			if r := recover(); r != nil {
				fmt.Println("Panic while advancing job: ", r)
				fmt.Println("Stack Trace:")
				debug.PrintStack()

				// Update the job stage and send a Discord notification
				jobState.Params[manager.JobParam_Error] = string(debug.Stack())[:1024]
				if err := m.updateJobStage(jobState, manager.JobStage_Failed); err != nil {
					log.Printf("advanceJob: job update failed after panic: %v, %s", err, manager.PrintJob(jobState))
				}
			}
		}()

		currentJobStage := jobState.Stage
		if job, err := m.prepareJob(jobState); err != nil {
			log.Printf("advanceJob: job generation failed: %v, %s", err, manager.PrintJob(jobState))
		} else if newJobState, err := job.AdvanceJob(); err != nil {
			// Advancing should automatically update the cache and database in case of failures
			log.Printf("advanceJob: job advancement failed: %v, %s", err, manager.PrintJob(jobState))
		} else if newJobState.Stage != currentJobStage {
			log.Printf("advanceJob: next job state: %s", manager.PrintJob(newJobState))
			m.postProcessJob(newJobState)
		}
	}()
}

func (m *JobManager) postProcessJob(jobState manager.JobState) {
	switch jobState.Type {
	case manager.JobType_Deploy:
		{
			switch jobState.Stage {
			// For completed deployments, also add a smoke test job 5 minutes in the future to allow the deployment to
			// stabilize.
			case manager.JobStage_Completed:
				{
					if _, err := m.NewJob(manager.JobState{
						Ts:   time.Now().Add(manager.DefaultWaitTime),
						Type: manager.JobType_TestSmoke,
						Params: map[string]interface{}{
							manager.JobParam_Source: manager.ServiceName,
						},
					}); err != nil {
						log.Printf("postProcessJob: failed to queue smoke tests after deploy: %v, %s", err, manager.PrintJob(jobState))
					}
				}
			// For failed deployments, rollback to the previously deployed commit hash.
			case manager.JobStage_Failed:
				{
					// Only rollback if this wasn't already a rollback attempt that failed
					if rollback, _ := jobState.Params[manager.JobParam_Rollback].(bool); !rollback {
						if _, err := m.NewJob(manager.JobState{
							Type: manager.JobType_Deploy,
							Params: map[string]interface{}{
								manager.JobParam_Component: jobState.Params[manager.JobParam_Component],
								manager.JobParam_Rollback:  true,
								// Make the job lookup the last successfully deployed commit hash from the database
								manager.JobParam_Sha: ".",
								// No point in waiting for other jobs to complete before redeploying a working image
								manager.JobParam_Force:  true,
								manager.JobParam_Source: manager.ServiceName,
							},
						}); err != nil {
							log.Printf("postProcessJob: failed to queue rollback after failed deploy: %v, %s", err, manager.PrintJob(jobState))
						}
					}
				}
			}
		}
	}
}

func (m *JobManager) prepareJob(jobState manager.JobState) (manager.Job, error) {
	var job manager.Job
	var genErr error = nil
	switch jobState.Type {
	case manager.JobType_Deploy:
		job, genErr = jobs.DeployJob(m.db, m.d, m.repo, m.notifs, jobState)
	case manager.JobType_Anchor:
		job = jobs.AnchorJob(m.db, m.d, m.notifs, jobState)
	case manager.JobType_TestE2E:
		job = jobs.E2eTestJob(m.db, m.d, m.notifs, jobState)
	case manager.JobType_TestSmoke:
		job = jobs.SmokeTestJob(m.db, m.d, m.notifs, jobState)
	default:
		genErr = fmt.Errorf("prepareJob: unknown job type: %s", manager.PrintJob(jobState))
	}
	if genErr != nil {
		jobState.Params[manager.JobParam_Error] = genErr
		if updErr := m.updateJobStage(jobState, manager.JobStage_Failed); updErr != nil {
			log.Printf("prepareJob: job update failed: %v, %s", updErr, manager.PrintJob(jobState))
		}
	}
	return job, genErr
}

func (m *JobManager) updateJobStage(jobState manager.JobState, jobStage manager.JobStage) error {
	jobState.Stage = jobStage
	// Update the job in the database before sending any notification - we should just come back and try again.
	if err := m.db.AdvanceJob(jobState); err != nil {
		log.Printf("updateJobStage: failed to update %s job: %v, %s", jobStage, err, manager.PrintJob(jobState))
		return err
	}
	log.Printf("updateJobStage: %s job: %s", jobStage, manager.PrintJob(jobState))
	m.notifs.NotifyJob(jobState)
	return nil
}
