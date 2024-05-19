package jobs

import (
	"fmt"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

// Allow up to 3 hours for anchor workers to run
const AnchorStalledTime = 3 * time.Hour

var _ manager.JobSm = &anchorJob{}

type anchorJob struct {
	baseJob
	env string
	d   manager.Deployment
}

func AnchorJob(jobState job.JobState, db manager.Database, notifs manager.Notifs, d manager.Deployment) manager.JobSm {
	return &anchorJob{baseJob{jobState, db, notifs}, os.Getenv(manager.EnvVar_Env), d}
}

func (a anchorJob) Advance() (job.JobState, error) {
	now := time.Now()
	switch a.state.Stage {
	case job.JobStage_Queued:
		{
			// No preparation needed so advance the job directly to "dequeued".
			//
			// Advance the timestamp by a tiny amount so that the "dequeued" event remains at the same position on the
			// timeline as the "queued" event but still ahead of it.
			return a.advance(job.JobStage_Dequeued, a.state.Ts.Add(time.Nanosecond), nil)
		}
	case job.JobStage_Dequeued:
		{
			if taskId, err := a.launchWorker(); err != nil {
				return a.advance(job.JobStage_Failed, now, err)
			} else {
				// Record the worker task identifier and its start time
				a.state.Params[job.JobParam_Id] = taskId
				a.state.Params[job.JobParam_Start] = float64(time.Now().UnixNano())
				return a.advance(job.JobStage_Started, now, nil)
			}
		}
	case job.JobStage_Started:
		{
			if started, err := a.checkWorker(true); err != nil {
				return a.advance(job.JobStage_Failed, now, err)
			} else if started {
				return a.advance(job.JobStage_Waiting, now, nil)
			} else {
				// Return so we come back again to check
				return a.state, nil
			}
		}
	case job.JobStage_Waiting:
		{
			if stopped, err := a.checkWorker(false); err != nil {
				return a.advance(job.JobStage_Failed, now, err)
			} else if stopped {
				return a.advance(job.JobStage_Completed, now, nil)
			} else if delayed, _ := a.state.Params[job.AnchorJobParam_Delayed].(bool); !delayed && job.IsTimedOut(a.state, AnchorStalledTime/2) {
				// If the job has been running for > 1.5 hours, mark it "delayed".
				a.state.Params[job.AnchorJobParam_Delayed] = true
				return a.advance(job.JobStage_Waiting, now, nil)
			} else if stalled, _ := a.state.Params[job.AnchorJobParam_Stalled].(bool); !stalled && job.IsTimedOut(a.state, AnchorStalledTime) {
				// If the job has been running for > 3 hours, mark it "stalled".
				a.state.Params[job.AnchorJobParam_Stalled] = true
				return a.advance(job.JobStage_Waiting, now, nil)
			} else {
				// Return so we come back again to check
				return a.state, nil
			}
		}
	default:
		{
			return a.advance(job.JobStage_Failed, now, fmt.Errorf("anchorJob: unexpected state: %s", manager.PrintJob(a.state)))
		}
	}
}

func (a anchorJob) launchWorker() (string, error) {
	var overrides map[string]string = nil
	// Check if this is a CASv5 anchor job
	if manager.IsV5WorkerJob(a.state) {
		if parsedOverrides, found := a.state.Params[job.AnchorJobParam_Overrides].(map[string]interface{}); found {
			overrides = make(map[string]string, len(parsedOverrides))
			for k, v := range parsedOverrides {
				overrides[k] = v.(string)
			}
		}
	}
	if taskId, err := a.d.LaunchTask(
		"ceramic-"+a.env+"-cas",
		"ceramic-"+a.env+"-cas-anchor",
		"cas_anchor",
		"/ceramic-"+a.env+"-cas/anchor_network_configuration",
		overrides); err != nil {
		return "", err
	} else {
		return taskId, nil
	}
}

func (a anchorJob) checkWorker(expectedToBeRunning bool) (bool, error) {
	if status, exitCode, err := a.d.CheckTask("ceramic-"+a.env+"-cas", "", expectedToBeRunning, false, a.state.Params[job.JobParam_Id].(string)); err != nil {
		return false, err
	} else if status {
		// If a non-zero exit code was present, the worker failed to complete successfully.
		if (exitCode != nil) && (*exitCode != 0) {
			return false, fmt.Errorf("anchorJob: worker exited with code %d", *exitCode)
		}
		return true, nil
	} else if expectedToBeRunning && job.IsTimedOut(a.state, manager.DefaultWaitTime) { // Worker did not start in time
		return false, manager.Error_StartupTimeout
	} else {
		return false, nil
	}
}
