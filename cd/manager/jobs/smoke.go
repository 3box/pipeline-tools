package jobs

import (
	"fmt"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

// Allow up to 5 minutes for smoke tests to run
const SmokeTestsWaitTime = 5 * time.Minute

var _ manager.Job = &smokeTestJob{}

type smokeTestJob struct {
	state manager.JobState
	db    manager.Database
	api   manager.ApiGw
}

func SmokeTestJob(db manager.Database, api manager.ApiGw, jobState manager.JobState) *smokeTestJob {
	return &smokeTestJob{jobState, db, api}
}

func (s smokeTestJob) AdvanceJob() error {
	if s.state.Stage == manager.JobStage_Queued {
		resourceId := os.Getenv("SMOKE_TEST_RESOURCE_ID")
		restApiId := os.Getenv("SMOKE_TEST_REST_API_ID")
		if _, err := s.api.Invoke("GET", resourceId, restApiId, ""); err != nil {
			return fmt.Errorf("smokeTestJob: api gateway call failed: %v", err)
		}
		s.state.Stage = manager.JobStage_Started
	} else if time.Now().Add(-SmokeTestsWaitTime).After(s.state.Ts) {
		// Since we're not monitoring for smoke test completion, give the tests some time to complete.
		s.state.Stage = manager.JobStage_Completed
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return fmt.Errorf("smokeTestJob: unexpected state: %s", manager.PrintJob(s.state))
	}
	s.state.Ts = time.Now()
	return s.db.UpdateJob(s.state)
}
