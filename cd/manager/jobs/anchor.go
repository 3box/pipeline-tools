package jobs

import (
	"fmt"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

// Allow up to 6 hours for anchor workers to run
const AnchorFailureTime = 6 * time.Hour

var _ manager.Job = &anchorJob{}

type anchorJob struct {
	state manager.JobState
	db    manager.Database
	d     manager.Deployment
	env   string
}

func AnchorJob(db manager.Database, d manager.Deployment, jobState manager.JobState) *anchorJob {
	return &anchorJob{jobState, db, d, os.Getenv("ENV")}
}

func (a anchorJob) AdvanceJob() error {
	if a.state.Stage == manager.JobStage_Queued {
		// Launch anchor worker
		if id, err := a.d.LaunchService(
			"ceramic-"+a.env+"-cas",
			"ceramic-"+a.env+"-cas-anchor",
			"ceramic-"+a.env+"-cas-anchor",
			"cas_anchor",
			nil); err != nil {
			a.state.Stage = manager.JobStage_Failed
		} else {
			// Update the job stage and spawned task identifier
			a.state.Stage = manager.JobStage_Started
			a.state.Params["id"] = id
		}
	} else if time.Now().Add(-AnchorFailureTime).After(a.state.Ts) {
		a.state.Stage = manager.JobStage_Failed
	} else if a.state.Stage == manager.JobStage_Started {
		if running, err := a.d.CheckTask(true, "ceramic-"+a.env+"-cas", a.state.Params["id"].(string)); err != nil {
			a.state.Stage = manager.JobStage_Failed
		} else if running {
			a.state.Stage = manager.JobStage_Waiting
		} else {
			// Return so we come back again to check
			return nil
		}
	} else if a.state.Stage == manager.JobStage_Waiting {
		if stopped, err := a.d.CheckTask(false, "ceramic-"+a.env+"-cas", a.state.Params["id"].(string)); err != nil {
			a.state.Stage = manager.JobStage_Failed
		} else if stopped {
			a.state.Stage = manager.JobStage_Completed
		} else {
			// Return so we come back again to check
			return nil
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return fmt.Errorf("anchorJob: unexpected state: %s", manager.PrintJob(a.state))
	}
	a.state.Ts = time.Now()
	return a.db.UpdateJob(a.state)
}
