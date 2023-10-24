package jobs

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

// Allow up to 3 hours for anchor workers to run
const AnchorStalledTime = 3 * time.Hour

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
		// No preparation needed so advance the job directly to "dequeued"
		a.state.Stage = manager.JobStage_Dequeued
		// Don't update the timestamp here so that the "dequeued" event remains at the same position on the timeline as
		// the "queued" event.
		return a.state, a.db.AdvanceJob(a.state)
	} else if a.state.Stage == manager.JobStage_Dequeued {
		var overrides map[string]string = nil
		// Check if this is a CASv5 anchor job
		if manager.IsV5WorkerJob(a.state) {
			if parsedOverrides, found := a.state.Params[manager.JobParam_Overrides].(map[string]interface{}); found {
				overrides = make(map[string]string, len(parsedOverrides))
				for k, v := range parsedOverrides {
					overrides[k] = v.(string)
				}
			}
		}
		// Launch anchor worker
		if id, err := a.d.LaunchTask(
			"ceramic-"+a.env+"-cas",
			"ceramic-"+a.env+"-cas-anchor",
			"cas_anchor",
			"/ceramic-"+a.env+"-cas/anchor_network_configuration",
			overrides); err != nil {
			a.state.Stage = manager.JobStage_Failed
			a.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("anchorJob: error starting task: %v, %s", err, manager.PrintJob(a.state))
		} else {
			// Update the job stage and spawned task identifier
			a.state.Stage = manager.JobStage_Started
			a.state.Params[manager.JobParam_Id] = id
			a.state.Params[manager.JobParam_Start] = time.Now().UnixNano()
		}
	} else if a.state.Stage == manager.JobStage_Started {
		if running, err := a.d.CheckTask("ceramic-"+a.env+"-cas", "", true, false, a.state.Params[manager.JobParam_Id].(string)); err != nil {
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
		if stopped, err := a.d.CheckTask("ceramic-"+a.env+"-cas", "", false, false, a.state.Params[manager.JobParam_Id].(string)); err != nil {
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
	a.state.Ts = time.Now()
	a.notifs.NotifyJob(a.state)
	return a.state, a.db.AdvanceJob(a.state)
}
