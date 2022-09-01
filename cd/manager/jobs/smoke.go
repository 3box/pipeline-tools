package jobs

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

// Allow up to 5 minutes for smoke tests to run
const SmokeTestsWaitTime = 5 * time.Minute

var _ manager.Job = &smokeTestJob{}

type smokeTestJob struct {
	state  manager.JobState
	db     manager.Database
	api    manager.ApiGw
	notifs manager.Notifs
}

func SmokeTestJob(db manager.Database, api manager.ApiGw, notifs manager.Notifs, jobState manager.JobState) *smokeTestJob {
	return &smokeTestJob{jobState, db, api, notifs}
}

func (s smokeTestJob) AdvanceJob() (manager.JobState, error) {
	if s.state.Stage == manager.JobStage_Queued {
		resourceId := os.Getenv("SMOKE_TEST_RESOURCE_ID")
		restApiId := os.Getenv("SMOKE_TEST_REST_API_ID")
		if _, err := s.api.Invoke("GET", resourceId, restApiId, ""); err != nil {
			s.state.Stage = manager.JobStage_Failed
			s.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("smokeTestJob: error starting tests: %v, %s", err, manager.PrintJob(s.state))
		} else {
			s.state.Stage = manager.JobStage_Started
		}
	} else if s.state.Stage == manager.JobStage_Started {
		if time.Now().Add(-SmokeTestsWaitTime).After(s.state.Ts) {
			// Since we're not monitoring for smoke test completion, give the tests some time to complete.
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
	return s.state, s.db.UpdateJob(s.state)
}
