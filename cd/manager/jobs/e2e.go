package jobs

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

// Allow up to 4 hours for E2E tests to run
const FailureTime = 4 * time.Hour

var _ manager.Job = &e2eTestJob{}

type e2eTestJob struct {
	state  manager.JobState
	db     manager.Database
	d      manager.Deployment
	notifs manager.Notifs
}

func E2eTestJob(db manager.Database, d manager.Deployment, notifs manager.Notifs, jobState manager.JobState) *e2eTestJob {
	return &e2eTestJob{jobState, db, d, notifs}
}

func (e e2eTestJob) AdvanceJob() (manager.JobState, error) {
	if e.state.Stage == manager.JobStage_Queued {
		if err := e.startE2eTests(); err != nil {
			e.state.Stage = manager.JobStage_Failed
			e.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("e2eTestJob: error starting tests: %v, %s", err, manager.PrintJob(e.state))
		} else {
			e.state.Stage = manager.JobStage_Started
		}
	} else if time.Now().Add(-FailureTime).After(e.state.Ts) {
		e.state.Stage = manager.JobStage_Failed
		e.state.Params[manager.JobParam_Error] = manager.Error_Timeout
		log.Printf("e2eTestJob: job timed out: %s", manager.PrintJob(e.state))
	} else if e.state.Stage == manager.JobStage_Started {
		// Check if all suites started successfully
		if running, err := e.checkE2eTests(true); err != nil {
			e.state.Stage = manager.JobStage_Failed
			e.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("e2eTestJob: error checking tests running status: %v, %s", err, manager.PrintJob(e.state))
		} else if running {
			e.state.Stage = manager.JobStage_Waiting
		} else {
			// Return so we come back again to check
			return e.state, nil
		}
	} else if e.state.Stage == manager.JobStage_Waiting {
		// Check if all suites completed
		if stopped, err := e.checkE2eTests(false); err != nil {
			e.state.Stage = manager.JobStage_Failed
			e.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("e2eTestJob: error checking tests stopped status: %v, %s", err, manager.PrintJob(e.state))
		} else if stopped {
			e.state.Stage = manager.JobStage_Completed
		} else {
			// Return so we come back again to check
			return e.state, nil
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return e.state, fmt.Errorf("anchorJob: unexpected state: %s", manager.PrintJob(e.state))
	}
	e.notifs.NotifyJob(e.state)
	return e.state, e.db.UpdateJob(e.state)
}

func (e e2eTestJob) startE2eTests() error {
	if err := e.startE2eTest(manager.E2eTest_PrivatePublic); err != nil {
		return err
	} else if err = e.startE2eTest(manager.E2eTest_LocalClientPublic); err != nil {
		return err
	} else {
		return e.startE2eTest(manager.E2eTest_LocalNodePrivate)
	}
}

func (e e2eTestJob) startE2eTest(config string) error {
	if id, err := e.d.LaunchServiceTask(
		"ceramic-qa-tests",
		"ceramic-qa-tests-e2e_tests",
		"ceramic-qa-tests-e2e_tests",
		"e2e_tests",
		map[string]string{
			"NODE_ENV":              config,
			"ETH_RPC_URL":           os.Getenv("BLOCKCHAIN_RPC_URL"),
			"AWS_ACCESS_KEY_ID":     os.Getenv("E2E_AWS_ACCESS_KEY_ID"),
			"AWS_SECRET_ACCESS_KEY": os.Getenv("E2E_AWS_SECRET_ACCESS_KEY"),
			"AWS_REGION":            os.Getenv("AWS_REGION"),
		}); err != nil {
		return err
	} else {
		e.state.Params[config] = id
		return nil
	}
}

func (e e2eTestJob) checkE2eTests(isRunning bool) (bool, error) {
	if privatePublic, err := e.d.CheckTask(isRunning, "ceramic-qa-tests", e.state.Params[manager.E2eTest_PrivatePublic].(string)); err != nil {
		return false, err
	} else if localClientPublic, err := e.d.CheckTask(isRunning, "ceramic-qa-tests", e.state.Params[manager.E2eTest_LocalClientPublic].(string)); err != nil {
		return false, err
	} else if localNodePrivate, err := e.d.CheckTask(isRunning, "ceramic-qa-tests", e.state.Params[manager.E2eTest_LocalNodePrivate].(string)); err != nil {
		return false, err
	} else if privatePublic && localClientPublic && localNodePrivate {
		return true, nil
	}
	return false, nil
}
