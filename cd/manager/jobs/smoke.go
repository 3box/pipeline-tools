package jobs

import (
	"fmt"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

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
			return fmt.Errorf("smoke: api gateway call failed: %v", err)
		}
		// Update the job stage and timestamp
		s.state.Stage = manager.JobStage_Completed
		s.state.Ts = time.Now()
		if err := s.db.UpdateJob(s.state); err != nil {
			return err
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return fmt.Errorf("smokeTestJob: unexpected state: %v", s.state)
	}
	return nil
}
