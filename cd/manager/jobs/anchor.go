package jobs

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

// Allow up to 3 hours for anchor workers to run
const AnchorFailureTime = 3 * time.Hour
const TaskIdParam = "id"
const StartTimeParam = "start"

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
			a.state.Params[StartTimeParam] = time.Now().UnixMilli()
		}
	} else if a.isTimedOut(AnchorFailureTime) {
		a.state.Stage = manager.JobStage_Failed
		a.state.Params[manager.JobParam_Error] = manager.Error_Timeout
		log.Printf("anchorJob: job timed out: %s", manager.PrintJob(a.state))
	} else if a.state.Stage == manager.JobStage_Started {
		if running, err := a.d.CheckTask("ceramic-"+a.env+"-cas", "", true, false, a.state.Params[TaskIdParam].(string)); err != nil {
			a.state.Stage = manager.JobStage_Failed
			a.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("anchorJob: error checking task running status: %v, %s", err, manager.PrintJob(a.state))
		} else if running {
			a.state.Stage = manager.JobStage_Waiting
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
		} else if a.isTimedOut(AnchorFailureTime / 2) {
			// If the job has been running for 1.5 hours, mark it "delayed".
			a.state.Stage = manager.JobStage_Delayed
			log.Printf("anchorJob: job delayed: %s", manager.PrintJob(a.state))
		} else {
			// Return so we come back again to check
			return a.state, nil
		}
	} else if a.state.Stage == manager.JobStage_Delayed {
		if stopped, err := a.d.CheckTask("ceramic-"+a.env+"-cas", "", false, false, a.state.Params[TaskIdParam].(string)); err != nil {
			a.state.Stage = manager.JobStage_Failed
			a.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("anchorJob: error checking task stopped status: %v, %s", err, manager.PrintJob(a.state))
		} else if stopped {
			a.state.Stage = manager.JobStage_Completed
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

func (a anchorJob) isTimedOut(delay time.Duration) bool {
	// If no timestamp was stored, use the timestamp from the last update.
	startTime := a.state.Ts
	if s, found := a.state.Params[StartTimeParam].(float64); found {
		startTime = time.UnixMilli(int64(s))
	}
	return time.Now().Add(-delay).After(startTime)
}
