package manager

import (
	_ "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"time"
)

const DefaultTick = 10 * time.Second
const DefaultTtlDays = 1
const DefaultFailureTime = 30 * time.Minute

type JobType string

const (
	JobType_Deploy    JobType = "deploy"
	JobType_Anchor    JobType = "anchor"
	JobType_TestE2E   JobType = "test_e2e"
	JobType_TestSmoke JobType = "test_smoke"
)

type JobStage string

const (
	JobStage_Queued     JobStage = "queued"
	JobStage_Processing JobStage = "processing"
	JobStage_Failed     JobStage = "failed"
	JobStage_Completed  JobStage = "completed"
)

type JobEvent struct {
	Type   JobType
	Params map[string]interface{}
}

type JobState struct {
	Stage  JobStage               `dynamodbav:"stage"`
	Ts     time.Time              `dynamodbav:"ts"`
	Id     string                 `dynamodbav:"id"`
	Type   JobType                `dynamodbav:"type"`
	Params map[string]interface{} `dynamodbav:"params"`
}

type Job interface {
	AdvanceJob() error
}

type AdvanceJobFn func() error
type TestJobFn func() (bool, error)
type CancelJobFn func() error

type ApiGw interface {
	Invoke(string, string, string, string) (string, error)
}

type Database interface {
	InitializeJobs() error
	QueueJob(*JobState) error
	DequeueJob() (*JobState, error)
	UpdateJob(*JobState) error
	DeleteJob(*JobState) error
	JobById(string) *JobState
	JobsByStage(JobStage, ...JobType) map[string]*JobState
}

type Deployment interface {
	LaunchService(cluster, service, family, container string, overrides map[string]string) (string, error)
	CheckService(string, ...string) (bool, error)
	RestartService(string, string) error
	UpdateService(string, string) error
}

type Server interface {
	Setup(cluster, service, family, container string, overrides map[string]string) error
}

type Queue interface {
	NewJob(*JobState) error
	ProcessJobs()
}
