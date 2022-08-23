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
	cache      manager.Cache
	db         manager.Database
	deployment manager.Deployment
	apiGw      manager.ApiGw
	discord    webhook.Client
	waitGroup  *sync.WaitGroup
	shutdownCh chan bool
}

func NewJobManager(cache manager.Cache, db manager.Database, d manager.Deployment, a manager.ApiGw, shutdownCh chan bool) (JobManager, error) {
	discord := webhook.New(snowflake.GetEnv("DISCORD_WEBHOOK"), os.Getenv("DISCORD_TOKEN"))
	return JobManager{cache, db, d, a, discord, new(sync.WaitGroup), shutdownCh}, nil
}

func (jq JobManager) NewJob(jobState *manager.JobState) error {
	return jq.db.QueueJob(jobState)
}

func (jq JobManager) ProcessJobs() {
	// Create a ticker to poll the database for new jobs.
	tick := time.NewTicker(manager.DefaultTick)
	for {
		log.Println("queue: start processing jobs...")
		for {
			select {
			case <-jq.shutdownCh:
				log.Println("queue: stop processing jobs...")
				// TODO: Cleanup here
				tick.Stop()
				return
			case <-tick.C:
				if err := jq.advanceJobs(); err != nil {
					log.Printf("queue: error processing jobs: %v", err)
				}
			}
		}
	}
}

func (jq JobManager) advanceJobs() error {
	// Age out completed/failed jobs older than 1 day.
	staleJobsMatcher := func(js *manager.JobState) bool {
		return (js.Stage == manager.JobStage_Completed) || (js.Stage == manager.JobStage_Failed)
	}
	for _, jobState := range jq.cache.JobsByMatcher(staleJobsMatcher) {
		log.Printf("processJobs: aging out job: %v", jobState)
		// Delete the job from the cache
		jq.cache.DeleteJob(jobState.Id)
	}

	// Find all jobs in progress and advance their state before looking for new jobs.
	activeJobsMatcher := func(js *manager.JobState) bool {
		return js.Stage == manager.JobStage_Processing
	}
	for _, jobState := range jq.cache.JobsByMatcher(activeJobsMatcher) {
		log.Printf("processJobs: advance jobs in progress...")
		jq.advanceJob(jobState)
	}

	// TODO: See if we can pick up multiple jobs:
	// - any number of anchor workers
	// - one smoke test at a time (but in parallel with anchor workers, E2E tests)
	// - one E2E test at a time (but in parallel with anchor workers, smoke tests)

	// TODO: Collapse similar jobs:
	// - Collapse all smoke tests between deployments into a single run
	// - Collapse all E2E tests between deployments into a single run
	// - Collapse all similar deployments into a single run

	// Find a queued job and kick it off based on exclusion rules.
	activeDeployMatcher := func(js *manager.JobState) bool {
		return (js.Stage == manager.JobStage_Processing) && (js.Type == manager.JobType_Deploy)
	}
	if jobState, err := jq.db.DequeueJob(); err != nil {
		log.Printf("processJobs: job dequeue failed: %v", err)
	} else if jobState != nil {
		log.Printf("processJobs: found queued job...")
		// Make sure that no deployment is in progress before starting to process *any* new job. All other jobs can run
		// in parallel.
		deployJobs := jq.cache.JobsByMatcher(activeDeployMatcher)
		if len(deployJobs) == 0 {
			jq.advanceJob(jobState)
		} else {
			log.Printf("processJobs: deferring job due to deployment(s) in progress: %v, %v", jobState, deployJobs)
		}
	}
	// Wait for all goroutines to finish so that we're sure that all job advancements have been completed before we
	// iterate again. The ticker will automatically drop ticks then pick back up later if a round of processing takes
	// longer than 1 tick.
	jq.waitGroup.Wait()
	return nil
}

func (jq JobManager) advanceJob(jobState *manager.JobState) {
	jq.waitGroup.Add(1)
	go func() {
		defer jq.waitGroup.Done()
		if job, err := jq.generateJob(jobState); err != nil {
			log.Printf("advanceJob: job generation failed: %v, %v", jobState, err)
		} else if err = job.AdvanceJob(); err != nil {
			log.Printf("advanceJob: job advancement failed: %v, %v", jobState, err)
		}
	}()
}

func (jq JobManager) generateJob(jobState *manager.JobState) (manager.Job, error) {
	var job manager.Job
	switch jobState.Type {
	case manager.JobType_Deploy:
		return nil, fmt.Errorf("NewJob: unsupported job type: %v", jobState)
	case manager.JobType_Anchor:
		{
			job = jobs.AnchorJob(jq.db, jq.deployment, jobState)
		}
	case manager.JobType_TestE2E:
		return nil, fmt.Errorf("NewJob: unsupported job type: %v", jobState)
	case manager.JobType_TestSmoke:
		{
			job = jobs.SmokeTestJob(jq.db, jq.apiGw, jobState)
		}
	default:
		return nil, fmt.Errorf("NewJob: unknown job type: %v", jobState)
	}
	return job, nil
}
