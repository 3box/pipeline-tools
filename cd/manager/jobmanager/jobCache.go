package jobmanager

import (
	"sync"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Cache = &JobCache{}

type JobCache struct {
	jobs *sync.Map
}

func NewJobCache() manager.Cache {
	return &JobCache{new(sync.Map)}
}

func (c JobCache) WriteJob(jobState manager.JobState) {
	// Don't overwrite a newer state with an earlier one.
	if cachedJobState, found := c.JobById(jobState.Id); found && cachedJobState.Ts.After(jobState.Ts) {
		return
	}
	// Store a copy of the state, not a pointer to it.
	c.jobs.Store(jobState.Id, jobState)
}

func (c JobCache) DeleteJob(jobId string) {
	c.jobs.Delete(jobId)
}

func (c JobCache) JobById(jobId string) (manager.JobState, bool) {
	if job, found := c.jobs.Load(jobId); found {
		return job.(manager.JobState), true
	}
	return manager.JobState{}, false
}

func (c JobCache) JobsByMatcher(matcher func(jobStage manager.JobState) bool) []manager.JobState {
	jobs := make([]manager.JobState, 0, 0)
	c.jobs.Range(func(_, value interface{}) bool {
		jobState := value.(manager.JobState)
		if matcher(jobState) {
			jobs = append(jobs, jobState)
		}
		return true
	})
	return jobs
}
