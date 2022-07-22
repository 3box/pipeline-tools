package jobs

import (
	"fmt"
	"log"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Job = &anchorJob{}

type anchorJob struct {
	state      *manager.JobState
	db         manager.Database
	deployment manager.Deployment
}

func AnchorJob(db manager.Database, d manager.Deployment, jobState *manager.JobState) *anchorJob {
	return &anchorJob{jobState, db, d}
}

func (a anchorJob) AdvanceJob() error {
	if time.Now().AddDate(0, 0, -manager.DefaultTtlDays).After(a.state.Ts) {
		log.Printf("anchorJob: aging out: %v", a.state)
		if err := a.db.DeleteJob(a.state); err != nil {
			log.Printf("anchorJob: error deleting job: %v", a.state)
		}
	} else if a.state.Stage == manager.JobStage_Queued {
		// Launch anchor worker
		if id, err := a.deployment.LaunchService(
			"ceramic-dev-cas",
			"ceramic-dev-cas-anchor",
			"ceramic-dev-cas-anchor",
			"cas_anchor",
			nil); err != nil {
			return err
		} else {
			// Update the job stage and spawned task identifier
			a.state.Stage = manager.JobStage_Processing
			a.state.Params["id"] = id
		}
	} else if a.state.Stage == manager.JobStage_Processing {
		if done, err := a.deployment.CheckService("ceramic-dev-cas", a.state.Params["id"].(string)); err != nil {
			a.state.Stage = manager.JobStage_Failed
		} else if done {
			a.state.Stage = manager.JobStage_Completed
		} else if time.Now().Add(-manager.DefaultFailureTime).After(a.state.Ts) {
			a.state.Stage = manager.JobStage_Failed
		} else {
			// Return so we come back again to check
			return nil
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return fmt.Errorf("anchorJob: unexpected state: %v", a.state)
	}
	a.state.Ts = time.Now()
	if err := a.db.UpdateJob(a.state); err != nil {
		return err
	}
	return nil
}
