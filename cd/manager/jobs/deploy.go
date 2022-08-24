package jobs

import (
	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Job = &deployJob{}

type deployJob struct {
	state *manager.JobState
	db    manager.Database
	d     manager.Deployment
}

func DeployJob(db manager.Database, d manager.Deployment, jobState *manager.JobState) *deployJob {
	return &deployJob{jobState, db, d}
}

func (d deployJob) AdvanceJob() error {
	return nil
}
