package jobs

import (
	"fmt"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ manager.JobSm = &e2eTestJob{}

type e2eTestJob struct {
	baseJob
	d manager.Deployment
}

const (
	e2eTest_PrivatePublic     string = "private-public"
	e2eTest_LocalClientPublic string = "local_client-public"
	e2eTest_LocalNodePrivate  string = "local_node-private"
)

// Allow up to 4 hours for E2E tests to run
const e2eFailureTime = 4 * time.Hour

func E2eTestJob(jobState job.JobState, db manager.Database, notifs manager.Notifs, d manager.Deployment) manager.JobSm {
	return &e2eTestJob{baseJob{jobState, db, notifs}, d}
}

func (e e2eTestJob) Advance() (job.JobState, error) {
	now := time.Now()
	switch e.state.Stage {
	case job.JobStage_Queued:
		{
			// No preparation needed so advance the job directly to "dequeued".
			//
			// Advance the timestamp by a tiny amount so that the "dequeued" event remains at the same position on the
			// timeline as the "queued" event but still ahead of it.
			return e.advance(job.JobStage_Dequeued, e.state.Ts.Add(time.Nanosecond), nil)
		}
	case job.JobStage_Dequeued:
		{
			if err := e.startAllTests(); err != nil {
				return e.advance(job.JobStage_Failed, now, err)
			} else {
				e.state.Params[job.JobParam_Start] = float64(time.Now().UnixNano())
				return e.advance(job.JobStage_Started, now, nil)
			}
		}
	case job.JobStage_Started:
		{
			if running, err := e.checkAllTests(true); err != nil {
				return e.advance(job.JobStage_Failed, now, err)
			} else if running {
				return e.advance(job.JobStage_Waiting, now, nil)
			} else if job.IsTimedOut(e.state, manager.DefaultWaitTime) { // Tests did not start in time
				return e.advance(job.JobStage_Failed, now, manager.Error_StartupTimeout)
			} else {
				// Return so we come back again to check
				return e.state, nil
			}
		}
	case job.JobStage_Waiting:
		{
			if stopped, err := e.checkAllTests(false); err != nil {
				return e.advance(job.JobStage_Failed, now, err)
			} else if stopped {
				return e.advance(job.JobStage_Completed, now, nil)
			} else if job.IsTimedOut(e.state, e2eFailureTime) { // Tests did not finish in time
				return e.advance(job.JobStage_Failed, now, manager.Error_CompletionTimeout)
			} else {
				// Return so we come back again to check
				return e.state, nil
			}
		}
	default:
		{
			return e.advance(job.JobStage_Failed, now, fmt.Errorf("e2eTestJob: unexpected state: %s", manager.PrintJob(e.state)))
		}
	}
}

func (e e2eTestJob) startAllTests() error {
	if err := e.startTests(e2eTest_PrivatePublic); err != nil {
		return err
	} else if err = e.startTests(e2eTest_LocalClientPublic); err != nil {
		return err
	} else {
		return e.startTests(e2eTest_LocalNodePrivate)
	}
}

func (e e2eTestJob) startTests(config string) error {
	if id, err := e.d.LaunchServiceTask(
		"ceramic-qa-tests",
		"ceramic-qa-tests-e2e_tests",
		"ceramic-qa-tests-e2e_tests",
		"e2e_tests",
		map[string]string{
			"NODE_ENV":                      config,
			"ETH_RPC_URL":                   os.Getenv("BLOCKCHAIN_RPC_URL"),
			"AWS_ACCESS_KEY_ID":             os.Getenv("E2E_AWS_ACCESS_KEY_ID"),
			"AWS_SECRET_ACCESS_KEY":         os.Getenv("E2E_AWS_SECRET_ACCESS_KEY"),
			"AWS_REGION":                    os.Getenv("AWS_REGION"),
			"CERAMIC_NODE_PRIVATE_SEED_URL": os.Getenv("CERAMIC_NODE_PRIVATE_SEED_URL"),
		}); err != nil {
		return err
	} else {
		e.state.Params[config] = id
		return nil
	}
}

func (e e2eTestJob) checkAllTests(expectedToBeRunning bool) (bool, error) {
	if privatePublicStatus, err := e.checkTests(e.state.Params[e2eTest_PrivatePublic].(string), expectedToBeRunning); err != nil {
		return false, err
	} else if localClientPublicStatus, err := e.checkTests(e.state.Params[e2eTest_LocalClientPublic].(string), expectedToBeRunning); err != nil {
		return false, err
	} else if localNodePrivateStatus, err := e.checkTests(e.state.Params[e2eTest_LocalNodePrivate].(string), expectedToBeRunning); err != nil {
		return false, err
	} else if privatePublicStatus && localClientPublicStatus && localNodePrivateStatus {
		return true, nil
	}
	return false, nil
}

func (e e2eTestJob) checkTests(taskId string, expectedToBeRunning bool) (bool, error) {
	if status, err := e.d.CheckTask("ceramic-qa-tests", "", expectedToBeRunning, false, taskId); err != nil {
		return false, err
	} else if status {
		return true, nil
	} else if expectedToBeRunning && job.IsTimedOut(e.state, manager.DefaultWaitTime) { // Tests did not start in time
		return false, manager.Error_StartupTimeout
	} else if !expectedToBeRunning && job.IsTimedOut(e.state, e2eFailureTime) { // Tests did not finish in time
		return false, manager.Error_CompletionTimeout
	} else {
		return false, nil
	}
}
