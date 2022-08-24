package jobmanager

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/disgoorg/disgo/webhook"
	"github.com/disgoorg/snowflake/v2"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/jobs"
)

type JobManager struct {
	cache     manager.Cache
	db        manager.Database
	d         manager.Deployment
	apiGw     manager.ApiGw
	discord   webhook.Client
	waitGroup *sync.WaitGroup
}

func NewJobManager(cache manager.Cache, db manager.Database, d manager.Deployment, a manager.ApiGw) (JobManager, error) {
	discord := webhook.New(snowflake.GetEnv("DISCORD_WEBHOOK"), os.Getenv("DISCORD_TOKEN"))
	return JobManager{cache, db, d, a, discord, new(sync.WaitGroup)}, nil
}

func (m JobManager) NewJob(jobState *manager.JobState) error {
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
	// Age out completed/failed jobs older than 1 day.
	oldJobs := m.cache.JobsByMatcher(func(js *manager.JobState) bool {
		return ((js.Stage == manager.JobStage_Completed) || (js.Stage == manager.JobStage_Failed)) &&
			time.Now().AddDate(0, 0, -manager.DefaultTtlDays).After(js.Ts)
	})
	if len(oldJobs) > 0 {
		log.Printf("processJobs: aging out %d jobs...", len(oldJobs))
		for _, job := range oldJobs {
			// Delete the job from the cache
			log.Printf("processJobs: aging out job: %v", job)
			m.cache.DeleteJob(job.Id)
		}
	}
	// Find all jobs in progress and advance their state before looking for new jobs.
	activeJobs := m.cache.JobsByMatcher(func(js *manager.JobState) bool {
		return js.Stage == manager.JobStage_Processing
	})
	if len(activeJobs) > 0 {
		log.Printf("processJobs: advancing %d jobs in progress...", len(activeJobs))
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
		log.Printf("processJobs: dequeued %d jobs...", len(dequeuedJobs))
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

func (m JobManager) processDeployJobs(jobs []*manager.JobState) {
	// Check if there are any incompatible jobs in progress.
	firstJob := jobs[0]
	incompatibleJobs := m.cache.JobsByMatcher(func(js *manager.JobState) bool {
		// Match non-deploy jobs, or jobs for the same component (viz. Ceramic, IPFS, or CAS).
		return (js.Stage == manager.JobStage_Processing) &&
			((js.Type != manager.JobType_Deploy) || (js.Params[manager.DeployParam_Component] == firstJob.Params[manager.DeployParam_Component]))
	})
	if len(incompatibleJobs) == 0 {
		// Collapse similar, back-to-back deployments into a single run and kick it off.
		deployJobs := make(map[manager.EventParam]*manager.JobState, 0)
		for i := 0; i < len(jobs); i++ {
			// Break out of the loop as soon as we find a non-deploy job. We don't want to collapse deploys across other
			// types of jobs.
			if jobs[i].Type != manager.JobType_Deploy {
				break
			}
			// Replace an existing deploy job for a component with a newer one, or add a new job (hence a map).
			deployComponent := jobs[i].Params[manager.DeployParam_Component].(manager.EventParam)
			deployJobs[deployComponent] = jobs[i]
		}
		// Now advance all deploy jobs, order doesn't matter.
		for _, deployJob := range deployJobs {
			m.advanceJob(deployJob)
		}
	} else {
		log.Printf("processJobs: deferring deployment because one or more jobs are in progress: %v, %v", firstJob, incompatibleJobs)
	}
}

func (m JobManager) processNonDeployJobs(jobs []*manager.JobState) {
	// Check if there are any deploy jobs in progress
	deployJobs := m.cache.JobsByMatcher(func(js *manager.JobState) bool {
		return (js.Stage == manager.JobStage_Processing) && (js.Type == manager.JobType_Deploy)
	})
	if len(deployJobs) == 0 {
		// - Launch an anchor worker per anchor job between deployments
		// - Collapse all smoke tests between deployments into a single run
		// - Collapse all E2E tests between deployments into a single run
		anchorJobs := make([]*manager.JobState, 0, 0)
		testJobs := make(map[manager.JobType]*manager.JobState, 0)
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
		log.Printf("processJobs: deferring job because one or more deployments are in progress: %v, %v", jobs[0], deployJobs)
	}
}

func (m JobManager) advanceJob(jobState *manager.JobState) {
	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		log.Printf("advanceJob: advancing job: %v", jobState)
		if job, err := m.generateJob(jobState); err != nil {
			log.Printf("advanceJob: job generation failed: %v, %v", jobState, err)
		} else if err = job.AdvanceJob(); err != nil {
			log.Printf("advanceJob: job advancement failed: %v, %v", jobState, err)
		}
	}()
}

func (m JobManager) generateJob(jobState *manager.JobState) (manager.Job, error) {
	var job manager.Job
	switch jobState.Type {
	case manager.JobType_Deploy:
		job = jobs.DeployJob(m.db, m.d, jobState)
	case manager.JobType_Anchor:
		job = jobs.AnchorJob(m.db, m.d, jobState)
	case manager.JobType_TestE2E:
		job = jobs.E2eTestJob(m.db, m.d, jobState)
	case manager.JobType_TestSmoke:
		job = jobs.SmokeTestJob(m.db, m.apiGw, jobState)
	default:
		return nil, fmt.Errorf("generateJob: unknown job type: %v", jobState)
	}
	return job, nil
}
