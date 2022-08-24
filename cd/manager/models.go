package manager

import "time"

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

type EventParam string

const (
	DeployParam_Event     EventParam = "event"
	DeployParam_Component EventParam = "component"
	DeployParam_Sha       EventParam = "sha"
	DeployParam_ShaTag    EventParam = "shaTag"
	DeployParam_Version   EventParam = "version"
)

const (
	DeployComponent_Ceramic EventParam = "ceramic"
	DeployComponent_Ipfs    EventParam = "ipfs"
	DeployComponent_Cas     EventParam = "cas"
)

type JobEvent struct {
	Type   JobType
	Params map[string]interface{}
}

type JobState struct {
	Stage  JobStage                   `dynamodbav:"stage"`
	Ts     time.Time                  `dynamodbav:"ts"`
	Id     string                     `dynamodbav:"id"`
	Type   JobType                    `dynamodbav:"type"`
	Params map[EventParam]interface{} `dynamodbav:"params"`
}

type Job interface {
	AdvanceJob() error
}

type ApiGw interface {
	Invoke(string, string, string, string) (string, error)
}

type Database interface {
	InitializeJobs() error
	QueueJob(*JobState) error
	DequeueJobs() []*JobState
	UpdateJob(*JobState) error
}

type Cache interface {
	WriteJob(*JobState)
	DeleteJob(string)
	JobById(string) *JobState
	JobsByMatcher(func(*JobState) bool) map[string]*JobState
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

type Manager interface {
	NewJob(*JobState) error
	ProcessJobs()
}
