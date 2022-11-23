package jobs

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

// Allow up to 15 minutes for smoke tests to run
const SmokeTestFailureTime = 15 * time.Minute

const ClusterName = "ceramic-qa-tests"
const FamilyPrefix = "ceramic-qa-tests-smoke--"
const ContainerName = "ceramic-qa-tests-smoke"
const NetworkConfigurationParameter = "/ceramic-qa-tests-smoke/network_configuration"

var _ manager.Job = &smokeTestJob{}

type smokeTestJob struct {
	state  manager.JobState
	db     manager.Database
	d      manager.Deployment
	notifs manager.Notifs
	env    string
}

func SmokeTestJob(db manager.Database, d manager.Deployment, notifs manager.Notifs, jobState manager.JobState) manager.Job {
	return &smokeTestJob{jobState, db, d, notifs, os.Getenv("ENV")}
}

func (s smokeTestJob) AdvanceJob() (manager.JobState, error) {
	if s.state.Stage == manager.JobStage_Queued {
		// Launch smoke test
		if id, err := s.d.LaunchTask(
			ClusterName,
			FamilyPrefix+s.env,
			ContainerName,
			NetworkConfigurationParameter,
			nil); err != nil {
			s.state.Stage = manager.JobStage_Failed
			s.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("smokeTestJob: error starting task: %v, %s", err, manager.PrintJob(s.state))
		} else {
			// Update the job stage and spawned task identifier
			s.state.Stage = manager.JobStage_Started
			s.state.Params[manager.JobParam_Id] = id
			s.state.Params[manager.JobParam_Start] = time.Now().UnixMilli()
		}
	} else if manager.IsTimedOut(s.state, SmokeTestFailureTime) { // Smoke test did not finish in time
		s.state.Stage = manager.JobStage_Failed
		s.state.Params[manager.JobParam_Error] = manager.Error_Timeout
		log.Printf("smokeTestJob: job run timed out: %s", manager.PrintJob(s.state))
	} else if s.state.Stage == manager.JobStage_Started {
		if running, err := s.d.CheckTask(ClusterName, "", true, false, s.state.Params[manager.JobParam_Id].(string)); err != nil {
			s.state.Stage = manager.JobStage_Failed
			s.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("smokeTestJob: error checking task running status: %v, %s", err, manager.PrintJob(s.state))
		} else if running {
			s.state.Stage = manager.JobStage_Waiting
		} else if manager.IsTimedOut(s.state, manager.DefaultWaitTime) { // Smoke test did not start running in time
			s.state.Stage = manager.JobStage_Failed
			s.state.Params[manager.JobParam_Error] = manager.Error_Timeout
			log.Printf("smokeTestJob: job startup timed out: %s", manager.PrintJob(s.state))
		} else {
			// Return so we come back again to check
			return s.state, nil
		}
	} else if s.state.Stage == manager.JobStage_Waiting {
		if stopped, err := s.d.CheckTask(ClusterName, "", false, false, s.state.Params[manager.JobParam_Id].(string)); err != nil {
			s.state.Stage = manager.JobStage_Failed
			s.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("smokeTestJob: error checking task stopped status: %v, %s", err, manager.PrintJob(s.state))
		} else if stopped {
			s.state.Stage = manager.JobStage_Completed
		} else {
			// Return so we come back again to check
			return s.state, nil
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return s.state, fmt.Errorf("smokeTestJob: unexpected state: %s", manager.PrintJob(s.state))
	}
	s.notifs.NotifyJob(s.state)
	return s.state, s.db.AdvanceJob(s.state)
}
