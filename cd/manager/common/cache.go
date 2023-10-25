package common

import (
	"sync"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ manager.Cache = &JobCache{}

type JobCache struct {
	jobs *sync.Map
}

func NewJobCache() manager.Cache {
	return &JobCache{new(sync.Map)}
}

func (c JobCache) WriteJob(jobState job.JobState) {
	// Don't overwrite a newer state with an earlier one.
	if cachedJobState, found := c.JobById(jobState.Job); found && cachedJobState.Ts.After(jobState.Ts) {
		return
	}
	// Store a copy of the state, not a pointer to it.
	c.jobs.Store(jobState.Job, jobState)
}

func (c JobCache) DeleteJob(jobId string) {
	c.jobs.Delete(jobId)
}

func (c JobCache) JobById(jobId string) (job.JobState, bool) {
	if j, found := c.jobs.Load(jobId); found {
		return j.(job.JobState), true
	}
	return job.JobState{}, false
}

func (c JobCache) JobsByMatcher(matcher func(jobStage job.JobState) bool) []job.JobState {
	jobs := make([]job.JobState, 0, 0)
	c.jobs.Range(func(_, value interface{}) bool {
		jobState := value.(job.JobState)
		if matcher(jobState) {
			jobs = append(jobs, jobState)
		}
		return true
	})
	return jobs
}
