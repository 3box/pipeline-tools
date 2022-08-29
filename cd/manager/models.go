package manager

import (
	"encoding/json"
	"fmt"
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
	JobStage_Queued    JobStage = "queued"
	JobStage_Skipped   JobStage = "skipped"
	JobStage_Started   JobStage = "started"
	JobStage_Waiting   JobStage = "waiting"
	JobStage_Delayed   JobStage = "delayed"
	JobStage_Failed    JobStage = "failed"
	JobStage_Completed JobStage = "completed"
)

type NotifTitle string

const (
	NotifTitle_Deploy    = "Deployment"
	NotifTitle_Anchor    = "Anchor Worker"
	NotifTitle_TestE2E   = "E2E Tests"
	NotifTitle_TestSmoke = "Smoke Tests"
)

const (
	EnvType_Dev  string = "dev"
	EnvType_Qa   string = "qa"
	EnvType_Tnet string = "tnet"
	EnvType_Prod string = "prod"
)

const (
	EventParam_Component string = "component"
	EventParam_Sha       string = "sha"
	EventParam_ShaTag    string = "shaTag"
)

const (
	DeployComponent_Ceramic string = "js-ceramic"
	DeployComponent_Ipfs    string = "go-ipfs-daemon"
	DeployComponent_Cas     string = "ceramic-anchor-service"
)

const (
	E2eTest_PrivatePublic     string = "private-public"
	E2eTest_LocalClientPublic string = "local_client-public"
	E2eTest_LocalNodePrivate  string = "local_node-private"
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

type ApiGw interface {
	Invoke(string, string, string, string) (string, error)
}

type Database interface {
	InitializeJobs() error
	QueueJob(JobState) error
	DequeueJobs() []JobState
	UpdateJob(JobState) error
}

type Cache interface {
	WriteJob(JobState)
	DeleteJob(string)
	JobById(string) (JobState, bool)
	JobsByMatcher(func(JobState) bool) []JobState
}

type Deployment interface {
	LaunchService(cluster, service, family, container string, overrides map[string]string) (string, error)
	CheckTask(bool, string, ...string) (bool, error)
	UpdateService(string, string, string) (string, error)
	CheckService(string, string, string) (bool, error)
	PopulateLayout(string) (map[string]map[string]interface{}, error)
	GetRegistryUri(string) (string, error)
}

type Notifs interface {
	NotifyJob(...JobState)
}

type Server interface {
	Setup(cluster, service, family, container string, overrides map[string]string) error
}

type Manager interface {
	NewJob(JobState) error
	ProcessJobs(shutdownCh chan bool)
}

func PrintJob(jobStates ...JobState) string {
	prettyString := ""
	for _, jobState := range jobStates {
		prettyBytes, err := json.MarshalIndent(jobState, "", "  ")
		if err != nil {
			prettyString += fmt.Sprintf("\n%+v", jobState)
		}
		prettyString += "\n" + string(prettyBytes)
	}
	return prettyString
}
