package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const DefaultTick = 10 * time.Second
const DefaultTtlDays = 1
const DefaultFailureTime = 30 * time.Minute
const DefaultHttpWaitTime = 10 * time.Second

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
	EnvBranch_Dev  string = "develop"
	EnvBranch_Qa   string = "develop"
	EnvBranch_Tnet string = "release-candidate"
	EnvBranch_Prod string = "main"
)

const (
	JobParam_Component string = "component"
	JobParam_Sha       string = "sha"
	JobParam_Error     string = "error"
	JobParam_Layout    string = "layout"
	JobParam_Manual    string = "manual"
)

type DeployComponent string

const (
	DeployComponent_Ceramic DeployComponent = "ceramic"
	DeployComponent_Cas     DeployComponent = "cas"
	DeployComponent_Ipfs    DeployComponent = "ipfs"
)

type DeployRepo string

const (
	DeployRepo_Ceramic DeployRepo = "js-ceramic"
	DeployRepo_Cas     DeployRepo = "ceramic-anchor-service"
	DeployRepo_Ipfs    DeployRepo = "go-ipfs-daemon"
)

type DeployType string

const (
	DeployType_Service DeployType = "service"
	DeployType_Task    DeployType = "task"
)

const (
	ServiceSuffix_CeramicNode    string = "node"
	ServiceSuffix_CeramicGateway string = "gateway"
	ServiceSuffix_IpfsNode       string = "ipfs-nd"
	ServiceSuffix_IpfsGateway    string = "ipfs-gw"
	ServiceSuffix_CasApi         string = "api"
	ServiceSuffix_CasWorker      string = "anchor"
	ServiceSuffix_CasScheduler   string = "scheduler"
)

const (
	E2eTest_PrivatePublic     string = "private-public"
	E2eTest_LocalClientPublic string = "local_client-public"
	E2eTest_LocalNodePrivate  string = "local_node-private"
)

const (
	Error_Timeout string = "Timeout"
)

const (
	NotifField_CommitHashes string = "Commit Hashes"
	NotifField_JobId        string = "Job ID"
	NotifField_Time         string = "Time"
)

// Repository
const CommitHashRegex = "[0-9a-f]{40}"
const BuildHashTag = "sha_tag"
const BuildHashLatest = "latest"
const GitHubOrg = "ceramicnetwork"

// Miscellaneous
const ResourceTag = "Ceramic"
const ServiceName = "cd-manager"
const DefaultCasMaxAnchorWorkers = 1

// JobState represents the state of a job in the database
type JobState struct {
	Stage  JobStage               `dynamodbav:"stage"`
	Ts     time.Time              `dynamodbav:"ts"`
	Id     string                 `dynamodbav:"id"`
	Type   JobType                `dynamodbav:"type"`
	Params map[string]interface{} `dynamodbav:"params"`
}

// BuildState represents build/deploy commit hash information. This information is maintained in a legacy DynamoDB table
// used by our utility AWS Lambdas.
type BuildState struct {
	Key       DeployComponent        `dynamodbav:"key"`
	DeployTag string                 `dynamodbav:"deployTag"`
	BuildInfo map[string]interface{} `dynamodbav:"buildInfo"`
}

// Layout (as well as Cluster, TaskSet, and Task) are a generic representation of our service structure within an
// orchestration service (e.g. AWS ECS).
type Layout struct {
	Clusters map[string]*Cluster `dynamodbav:"clusters"`
	Repo     string              `dynamodbav:"repo,omitempty"` // Layout repo
}

type Cluster struct {
	ServiceTasks *TaskSet `dynamodbav:"serviceTasks,omitempty"`
	Tasks        *TaskSet `dynamodbav:"tasks,omitempty"`
	Repo         string   `dynamodbav:"repo,omitempty"` // Cluster repo override
}

type TaskSet struct {
	Tasks map[string]*Task `dynamodbav:"tasks"`
	Repo  string           `dynamodbav:"repo,omitempty"` // TaskSet repo override
}

type Task struct {
	Id   string `dynamodbav:"id"`
	Repo string `dynamodbav:"repo,omitempty"` // Task repo override
	Temp bool   `dynamodbav:"temp,omitempty"` // Whether or not the task is meant to go down once it has completed
}

// Job represents job state machine objects processed by the job manager
type Job interface {
	AdvanceJob() (JobState, error)
}

// ApiGw represents an API Gateway service containing APIs we wish to invoke directly, i.e. not through an API call
// (e.g. AWS API Gateway).
type ApiGw interface {
	Invoke(string, string, string, string) (string, error)
}

// Database represents a database service that can be used as a job queue (e.g. AWS DynamoDB). Most popular document
// databases provide the primitives for them to be used in this fashion.
type Database interface {
	InitializeJobs() error
	QueueJob(JobState) error
	DequeueJobs() []JobState
	AdvanceJob(JobState) error
	WriteJob(JobState) error
	UpdateBuildHash(DeployComponent, string) error
	UpdateDeployHash(DeployComponent, string) error
	GetBuildHashes() (map[DeployComponent]string, error)
	GetDeployHashes() (map[DeployComponent]string, error)
}

// Cache represents an in-memory cache for job states
type Cache interface {
	WriteJob(JobState)
	DeleteJob(string)
	JobById(string) (JobState, bool)
	JobsByMatcher(func(JobState) bool) []JobState
}

// Deployment represents a container orchestration service (e.g. AWS ECS)
type Deployment interface {
	LaunchServiceTask(string, string, string, string, map[string]string) (string, error)
	LaunchTask(string, string, string, string, map[string]string) (string, error)
	CheckTask(string, string, bool, bool, ...string) (bool, error)
	PopulateEnvLayout(DeployComponent) (*Layout, error)
	UpdateEnv(*Layout, string) error
	CheckEnv(*Layout) (bool, error)
}

// Notifs represents a notification service (e.g. Discord)
type Notifs interface {
	NotifyJob(...JobState)
}

// Manager represents the job manager, which is the central job orchestrator of this service.
type Manager interface {
	NewJob(JobState) error
	ProcessJobs(chan bool)
}

// Repository represents a git service hosting our repositories (e.g. GitHub)
type Repository interface {
	GetLatestCommitHash(DeployRepo, string) (string, error)
}

func PrintJob(jobStates ...JobState) string {
	prettyString := ""
	for _, jobState := range jobStates {
		prettyBytes, err := json.Marshal(jobState)
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

func EnvBranch(env EnvType) string {
	switch env {
	case EnvType_Dev:
		return EnvBranch_Dev
	case EnvType_Qa:
		return EnvBranch_Qa
	case EnvType_Tnet:
		return EnvBranch_Tnet
	case EnvType_Prod:
		return EnvBranch_Prod
	default:
		return ""
	}
}

func ComponentRepo(component DeployComponent) DeployRepo {
	switch component {
	case DeployComponent_Ceramic:
		return DeployRepo_Ceramic
	case DeployComponent_Cas:
		return DeployRepo_Cas
	case DeployComponent_Ipfs:
		return DeployRepo_Ipfs
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

func CeramicEnvPfx() string {
	return "ceramic-" + os.Getenv("ENV")
}
