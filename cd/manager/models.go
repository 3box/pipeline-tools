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

const (
	JobName_Deploy    = "Deployment"
	JobName_Anchor    = "Anchor Worker"
	JobName_TestE2E   = "E2E Tests"
	JobName_TestSmoke = "Smoke Tests"
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

type EnvType string

const (
	EnvType_Dev  EnvType = "dev"
	EnvType_Qa   EnvType = "qa"
	EnvType_Tnet EnvType = "tnet"
	EnvType_Prod EnvType = "prod"
)

const (
	EnvName_Dev  string = "dev"
	EnvName_Qa   string = "dev-qa"
	EnvName_Tnet string = "testnet-clay"
	EnvName_Prod string = "mainnet"
)

const (
	JobParam_Component string = "component"
	JobParam_Sha       string = "sha"
	JobParam_Error     string = "error"
)

type DeployComponent string

const (
	DeployComponent_Ceramic DeployComponent = "ceramic"
	DeployComponent_Cas     DeployComponent = "cas"
	DeployComponent_Ipfs    DeployComponent = "ipfs"
)

const (
	DeployRepo_Ceramic string = "js-ceramic"
	DeployRepo_Cas     string = "ceramic-anchor-service"
	DeployRepo_Ipfs    string = "go-ipfs-daemon"
)

const (
	E2eTest_PrivatePublic     string = "private-public"
	E2eTest_LocalClientPublic string = "local_client-public"
	E2eTest_LocalNodePrivate  string = "local_node-private"
)

const (
	Error_Timeout string = "Timeout"
)

type JobState struct {
	Stage  JobStage               `dynamodbav:"stage"`
	Ts     time.Time              `dynamodbav:"ts"`
	Id     string                 `dynamodbav:"id"`
	Type   JobType                `dynamodbav:"type"`
	Params map[string]interface{} `dynamodbav:"params"`
}

type BuildState struct {
	Key       DeployComponent        `dynamodbav:"key"`
	DeployTag string                 `dynamodbav:"deployTag"`
	BuildInfo map[string]interface{} `dynamodbav:"buildInfo"`
}

type Job interface {
	AdvanceJob() (JobState, error)
}

type ApiGw interface {
	Invoke(string, string, string, string) (string, error)
}

type Database interface {
	InitializeJobs() error
	QueueJob(JobState) error
	DequeueJobs() []JobState
	UpdateJob(JobState) error
	UpdateBuildHash(DeployComponent, string) error
	UpdateDeployHash(DeployComponent, string) error
	GetDeployHashes() (map[DeployComponent]string, error)
}

type Cache interface {
	WriteJob(JobState)
	DeleteJob(string)
	JobById(string) (JobState, bool)
	JobsByMatcher(func(JobState) bool) []JobState
}

type Deployment interface {
	LaunchService(cluster, service, family, container string, overrides map[string]string) (string, error)
	LaunchTask(cluster, family, container, vpcConfigParam string, overrides map[string]string) (string, error)
	CheckTask(bool, string, ...string) (bool, error)
	UpdateService(string, string, string) (string, error)
	CheckService(string, string, string) (bool, error)
	PopulateLayout(DeployComponent) (map[string]interface{}, error)
	GetRegistryUri(DeployComponent) (string, error)
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

func EnvName(env EnvType) string {
	switch env {
	case EnvType_Dev:
		return EnvName_Dev
	case EnvType_Qa:
		return EnvName_Qa
	case EnvType_Tnet:
		return EnvName_Tnet
	case EnvType_Prod:
		return EnvName_Prod
	default:
		return ""
	}
}

func JobName(job JobType) string {
	switch job {
	case JobType_Deploy:
		return JobName_Deploy
	case JobType_Anchor:
		return JobName_Anchor
	case JobType_TestE2E:
		return JobName_TestE2E
	case JobType_TestSmoke:
		return JobName_TestSmoke
	default:
		return ""
	}
}
