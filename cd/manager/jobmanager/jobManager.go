package jobmanager

import (
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/jobs"
)

type JobManager struct {
	cache     manager.Cache
	db        manager.Database
	d         manager.Deployment
	apiGw     manager.ApiGw
	notifs    manager.Notifs
	waitGroup *sync.WaitGroup
}

func NewJobManager(cache manager.Cache, db manager.Database, d manager.Deployment, a manager.ApiGw, n manager.Notifs) (JobManager, error) {
	return JobManager{cache, db, d, a, n, new(sync.WaitGroup)}, nil
}

func (m JobManager) NewJob(jobState manager.JobState) error {
	jobState.Stage = manager.JobStage_Queued
	jobState.Id = uuid.New().String()
	// Only set the time if it hadn't already been set by the caller.
	if jobState.Ts.IsZero() {
		jobState.Ts = time.Now()
	}
	if jobState.Params == nil {
		jobState.Params = make(map[string]interface{}, 0)
	}
	return m.db.QueueJob(jobState)
}

func (m JobManager) ProcessJobs(shutdownCh chan bool) {
	// Create a ticker to poll the database for new jobs.
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
				// Attempt to acquire the run token to ensure that no jobs are being processed while shutting down.
				<-runToken
				return
			case <-tick.C:
				// Acquire the run token so that no loop iterations can run in parallel (shouldn't happen), and so that
				// shutdown can't complete until a running iteration has finished.
				<-runToken
				m.advanceJobs()
				// Release the run token
				runToken <- true
			}
		}
	}
}

func (m JobManager) advanceJobs() {
	// Age out completed/failed/skipped jobs older than 1 day.
	oldJobs := m.cache.JobsByMatcher(func(js manager.JobState) bool {
		return m.isFinishedJob(js) && time.Now().AddDate(0, 0, -manager.DefaultTtlDays).After(js.Ts)
	})
	if len(oldJobs) > 0 {
		log.Printf("processJobs: aging out %d jobs...", len(oldJobs))
		for _, job := range oldJobs {
			// Delete the job from the cache
			log.Printf("processJobs: aging out job: %s", manager.PrintJob(job))
			m.cache.DeleteJob(job.Id)
		}
	}
	// Find all jobs in progress and advance their state before looking for new jobs.
	activeJobs := m.cache.JobsByMatcher(m.isActiveJob)
	if len(activeJobs) > 0 {
		log.Printf("processJobs: checking %d jobs in progress...", len(activeJobs))
		for _, job := range activeJobs {
			m.advanceJob(job)
		}
	}
	// Try to dequeue multiple jobs and collapse similar ones:
	// - one deploy at a time (deploys for different services are compatible, i.e. Ceramic, IPFS, CAS can be deployed in
	//   parallel)
	// - any number of anchor workers (compatible with with smoke/E2E tests)
	// - one smoke test at a time (compatible with anchor workers, E2E tests)
	// - one E2E test at a time (compatible with anchor workers, smoke tests)
	//
	// Loop over compatible dequeued jobs until we find an incompatible one and need to wait for existing jobs to
	// complete.
	dequeuedJobs := m.db.DequeueJobs()
	if len(dequeuedJobs) > 0 {
		log.Printf("processJobs: dequeued %d new jobs...", len(dequeuedJobs))
		// Decide how to proceed based on the first job from the list.
		if dequeuedJobs[0].Type == manager.JobType_Deploy {
			m.processDeployJobs(dequeuedJobs)
		} else {
			m.processNonDeployJobs(dequeuedJobs)
		}
	}
	// Wait for all of this iteration's goroutines to finish so that we're sure that all job advancements have been
	// completed before we iterate again. The ticker will automatically drop ticks then pick back up later if a round of
	// processing takes longer than 1 tick.
	m.waitGroup.Wait()
}

func (m JobManager) processDeployJobs(jobs []manager.JobState) {
	// Check if there are any incompatible jobs in progress.
	firstJob := jobs[0]
	incompatibleJobs := m.cache.JobsByMatcher(func(js manager.JobState) bool {
		// Match non-deploy jobs, or jobs for the same component (viz. Ceramic, IPFS, or CAS).
		return m.isActiveJob(js) &&
			((js.Type != manager.JobType_Deploy) || (js.Params[manager.JobParam_Component] == firstJob.Params[manager.JobParam_Component]))
	})
	if len(incompatibleJobs) == 0 {
		// Collapse similar, back-to-back deployments into a single run and kick it off.
		deployJobs := make(map[string]manager.JobState, 0)
		for i := 0; i < len(jobs); i++ {
			// Break out of the loop as soon as we find a non-deploy job. We don't want to collapse deploys across other
			// types of jobs.
			if jobs[i].Type != manager.JobType_Deploy {
				break
			}
			// Replace an existing deploy job for a component with a newer one, or add a new job (hence a map).
			deployComponent := jobs[i].Params[manager.JobParam_Component].(string)
			// Update the cache and database for every skipped job.
			if skippedJob, found := deployJobs[deployComponent]; found {
				if err := m.updateJobStage(skippedJob, manager.JobStage_Skipped); err != nil {
					log.Printf("processDeployJobs: could not update skipped job: %v, %s", err, manager.PrintJob(skippedJob))
					// Return from here so that no state is changed and the loop can restart cleanly. Any jobs already
					// skipped won't be picked up again, which is ok.
					return
				}
			}
			deployJobs[deployComponent] = jobs[i]
		}
		// Now advance all deploy jobs, order doesn't matter.
		for _, deployJob := range deployJobs {
			m.advanceJob(deployJob)
		}
	} else {
		log.Printf("processDeployJobs: deferring deployment because one or more jobs are in progress: %s, %s", manager.PrintJob(firstJob), manager.PrintJob(incompatibleJobs...))
	}
}

func (m JobManager) processNonDeployJobs(jobs []manager.JobState) {
	// Check if there are any deploy jobs in progress
	deployJobs := m.cache.JobsByMatcher(func(js manager.JobState) bool {
		return m.isActiveJob(js) && (js.Type == manager.JobType_Deploy)
	})
	if len(deployJobs) == 0 {
		// - Launch an anchor worker per anchor job between deployments
		// - Collapse all smoke tests between deployments into a single run
		// - Collapse all E2E tests between deployments into a single run
		anchorJobs := make([]manager.JobState, 0, 0)
		testJobs := make(map[manager.JobType]manager.JobState, 0)
		for i := 0; i < len(jobs); i++ {
			// Break out of the loop as soon as we find a deploy job. We don't want to collapse non-deploy jobs across
			// deploy jobs.
			if jobs[i].Type == manager.JobType_Deploy {
				break
			}
			// Save each anchor job (hence a list).
			jobType := jobs[i].Type
			if jobType == manager.JobType_Anchor {
				anchorJobs = append(anchorJobs, jobs[i])
			} else {
				// Update the cache and database for every skipped job.
				if skippedJob, found := testJobs[jobType]; found {
					if err := m.updateJobStage(skippedJob, manager.JobStage_Skipped); err != nil {
						log.Printf("processNonDeployJobs: could not update skipped job: %v, %s", err, manager.PrintJob(skippedJob))
						// Return from here so that no state is changed and the loop can restart cleanly. Any jobs
						// already skipped won't be picked up again, which is ok.
						return
					}
				}
				// Replace an existing test job with a newer one, or add a new job (hence a map).
				testJobs[jobType] = jobs[i]
			}
		}
		// Now advance all anchor/test jobs, order doesn't matter.
		for _, anchorJob := range anchorJobs {
			m.advanceJob(anchorJob)
		}
		for _, testJob := range testJobs {
			m.advanceJob(testJob)
		}
	} else {
		log.Printf("processNonDeployJobs: deferring job because one or more deployments are in progress: %s, %s", manager.PrintJob(jobs[0]), manager.PrintJob(deployJobs...))
	}
}

func (m JobManager) advanceJob(jobState manager.JobState) {
	m.waitGroup.Add(1)
	go func() {
		defer func() {
			m.waitGroup.Done()
			if r := recover(); r != nil {
				fmt.Println("Panic while advancing job: ", r)
				fmt.Println("Stack Trace:")
				debug.PrintStack()

				// Update the job stage and send a Discord notification.
				jobState.Params[manager.JobParam_Error] = string(debug.Stack())[:1024]
				if err := m.updateJobStage(jobState, manager.JobStage_Failed); err != nil {
					log.Printf("advanceJob: job update failed after panic: %v, %s", err, manager.PrintJob(jobState))
				}
			}
		}()

		currentJobStage := jobState.Stage
		if job, err := m.generateJob(jobState); err != nil {
			log.Printf("advanceJob: job generation failed: %v, %s", err, manager.PrintJob(jobState))
			if err = m.updateJobStage(jobState, manager.JobStage_Failed); err != nil {
				log.Printf("advanceJob: job update failed: %v, %s", err, manager.PrintJob(jobState))
			}
		} else if newJobState, err := job.AdvanceJob(); err != nil {
			// Advancing should automatically update the cache and database in case of failures.
			log.Printf("advanceJob: job advancement failed: %v, %s", err, manager.PrintJob(jobState))
		} else if newJobState.Stage != currentJobStage {
			log.Printf("advanceJob: next job state: %s", manager.PrintJob(newJobState))
			// For completed deployments, also add a smoke test job 5 minutes in the future to allow the deployment to
			// stabilize.
			if (newJobState.Type == manager.JobType_Deploy) && (newJobState.Stage == manager.JobStage_Completed) {
				if err = m.NewJob(manager.JobState{
					Ts:   time.Now().Add(5 * time.Minute),
					Type: manager.JobType_TestSmoke,
				}); err != nil {
					log.Printf("advanceJob: failed to queue smoke tests after deploy: %v, %s", err, manager.PrintJob(newJobState))
				}
			}
		}
	}()
}

func (m JobManager) generateJob(jobState manager.JobState) (manager.Job, error) {
	var job manager.Job
	var err error = nil
	switch jobState.Type {
	case manager.JobType_Deploy:
		job, err = jobs.DeployJob(m.db, m.d, m.notifs, jobState)
	case manager.JobType_Anchor:
		job = jobs.AnchorJob(m.db, m.d, m.notifs, jobState)
	case manager.JobType_TestE2E:
		job = jobs.E2eTestJob(m.db, m.d, m.notifs, jobState)
	case manager.JobType_TestSmoke:
		job = jobs.SmokeTestJob(m.db, m.apiGw, m.notifs, jobState)
	default:
		return nil, fmt.Errorf("generateJob: unknown job type: %s", manager.PrintJob(jobState))
	}
	return job, err
}

func (m JobManager) isFinishedJob(jobState manager.JobState) bool {
	return (jobState.Stage == manager.JobStage_Completed) || (jobState.Stage == manager.JobStage_Failed) || (jobState.Stage == manager.JobStage_Skipped)
}

func (m JobManager) isActiveJob(jobState manager.JobState) bool {
	return (jobState.Stage == manager.JobStage_Started) || (jobState.Stage == manager.JobStage_Waiting) || (jobState.Stage == manager.JobStage_Delayed)
}

func (m JobManager) updateJobStage(jobState manager.JobState, jobStage manager.JobStage) error {
	jobState.Stage = jobStage
	// Update the job in the database before sending any notification - we should just come back and try again.
	if err := m.db.UpdateJob(jobState); err != nil {
		return err
	}
	m.notifs.NotifyJob(jobState)
	return nil
}
