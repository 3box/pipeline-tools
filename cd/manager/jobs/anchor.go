package jobs

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

const AnchorStalledTime = 3 * time.Hour
const TaskIdParam = "id"

var _ manager.Job = &anchorJob{}

type anchorJob struct {
	state  manager.JobState
	db     manager.Database
	d      manager.Deployment
	notifs manager.Notifs
	env    string
}

func AnchorJob(db manager.Database, d manager.Deployment, notifs manager.Notifs, jobState manager.JobState) manager.Job {
	return &anchorJob{jobState, db, d, notifs, os.Getenv("ENV")}
}

func (a anchorJob) AdvanceJob() (manager.JobState, error) {
	if a.state.Stage == manager.JobStage_Queued {
		// Launch anchor worker
		if id, err := a.d.LaunchTask(
			"ceramic-"+a.env+"-cas",
			"ceramic-"+a.env+"-cas-anchor",
			"cas_anchor",
			"/ceramic-"+a.env+"-cas/anchor_network_configuration",
			nil); err != nil {
			a.state.Stage = manager.JobStage_Failed
			a.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("anchorJob: error starting task: %v, %s", err, manager.PrintJob(a.state))
		} else {
			// Update the job stage and spawned task identifier
			a.state.Stage = manager.JobStage_Started
			a.state.Params[TaskIdParam] = id
			a.state.Params[manager.JobParam_Start] = time.Now().UnixMilli()
		}
	} else if a.state.Stage == manager.JobStage_Started {
		if running, err := a.d.CheckTask("ceramic-"+a.env+"-cas", "", true, false, a.state.Params[TaskIdParam].(string)); err != nil {
			a.state.Stage = manager.JobStage_Failed
			a.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("anchorJob: error checking task running status: %v, %s", err, manager.PrintJob(a.state))
		} else if running {
			a.state.Stage = manager.JobStage_Waiting
		} else if manager.IsTimedOut(a.state, manager.DefaultWaitTime) { // Anchor worker did not start running in time
			a.state.Stage = manager.JobStage_Failed
			a.state.Params[manager.JobParam_Error] = manager.Error_Timeout
			log.Printf("anchorJob: job startup timed out: %s", manager.PrintJob(a.state))
		} else {
			// Return so we come back again to check
			return a.state, nil
		}
	} else if a.state.Stage == manager.JobStage_Waiting {
		if stopped, err := a.d.CheckTask("ceramic-"+a.env+"-cas", "", false, false, a.state.Params[TaskIdParam].(string)); err != nil {
			a.state.Stage = manager.JobStage_Failed
			a.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("anchorJob: error checking task stopped status: %v, %s", err, manager.PrintJob(a.state))
		} else if stopped {
			a.state.Stage = manager.JobStage_Completed
		} else if delayed, _ := a.state.Params[manager.JobParam_Delayed].(bool); !delayed && manager.IsTimedOut(a.state, AnchorStalledTime/2) {
			// If the job has been running for > 1.5 hours, mark it "delayed".
			a.state.Params[manager.JobParam_Delayed] = true
			log.Printf("anchorJob: job delayed: %s", manager.PrintJob(a.state))
		} else if stalled, _ := a.state.Params[manager.JobParam_Stalled].(bool); !stalled && manager.IsTimedOut(a.state, AnchorStalledTime) {
			// If the job has been running for > 3 hours, mark it "stalled".
			a.state.Params[manager.JobParam_Stalled] = true
			log.Printf("anchorJob: job stalled: %s", manager.PrintJob(a.state))
		} else {
			// Return so we come back again to check
			return a.state, nil
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return a.state, fmt.Errorf("anchorJob: unexpected state: %s", manager.PrintJob(a.state))
	}
	a.notifs.NotifyJob(a.state)
	return a.state, a.db.AdvanceJob(a.state)
}
