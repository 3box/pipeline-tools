package jobs

import (
	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Job = &e2eTestJob{}

type e2eTestJob struct {
	state *manager.JobState
	db    manager.Database
	d     manager.Deployment
}

func E2eTestJob(db manager.Database, d manager.Deployment, jobState *manager.JobState) *e2eTestJob {
	return &e2eTestJob{jobState, db, d}
}

func (e e2eTestJob) AdvanceJob() error {
	return nil
}
