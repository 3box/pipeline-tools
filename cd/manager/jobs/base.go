package jobs

import (
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

type baseJob struct {
	state  job.JobState
	db     manager.Database
	notifs manager.Notifs
}

func (b *baseJob) advance(jobStage job.JobStage, ts time.Time, err error) (job.JobState, error) {
	return manager.AdvanceJob(&b.state, jobStage, ts, err, b.db, b.notifs)
}
