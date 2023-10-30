package manager

import (
	"fmt"
	"time"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

const DefaultTick = 10 * time.Second
const DefaultTtlDays = 1
const DefaultFailureTime = 30 * time.Minute
const DefaultHttpWaitTime = 30 * time.Second
const DefaultWaitTime = 5 * time.Minute

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
	EnvBranch_Qa   string = "qa"
	EnvBranch_Tnet string = "release-candidate"
	EnvBranch_Prod string = "main"
)

type DeployComponent string

const (
	DeployComponent_Ceramic DeployComponent = "ceramic"
	DeployComponent_Cas     DeployComponent = "cas"
	DeployComponent_CasV5   DeployComponent = "casv5"
	DeployComponent_Ipfs    DeployComponent = "ipfs"
)

type DeployRepo string

const (
	DeployRepo_Ceramic DeployRepo = "js-ceramic"
	DeployRepo_Cas     DeployRepo = "ceramic-anchor-service"
	DeployRepo_CasV5   DeployRepo = "go-cas"
	DeployRepo_Ipfs    DeployRepo = "go-ipfs-daemon"
)

type DeployType string

const (
	DeployType_Service DeployType = "service"
	DeployType_Task    DeployType = "task"
)

const (
	ServiceSuffix_CeramicNode  string = "node"
	ServiceSuffix_IpfsNode     string = "ipfs-nd"
	ServiceSuffix_CasApi       string = "api"
	ServiceSuffix_CasWorker    string = "anchor"
	ServiceSuffix_CasScheduler string = "scheduler"
	ServiceSuffix_Elp          string = "elp"
)

const (
	E2eTest_PrivatePublic     string = "private-public"
	E2eTest_LocalClientPublic string = "local_client-public"
	E2eTest_LocalNodePrivate  string = "local_node-private"
)

const (
	ContainerName_CeramicNode    string = "ceramic_node"
	ContainerName_IpfsNode       string = "go-ipfs"
	ContainerName_CasApi         string = "cas_api"
	ContainerName_CasWorker      string = "cas_anchor"
	ContainerName_CasScheduler   string = "cas_scheduler"
	ContainerName_CasV5Scheduler string = "scheduler"
)

var (
	Error_StartupTimeout    = fmt.Errorf("startup timeout")
	Error_CompletionTimeout = fmt.Errorf("completion timeout")
)

const (
	NotifField_CommitHashes string = "Commit Hashes"
	NotifField_JobId        string = "Job ID"
	NotifField_Time         string = "Time"
	NotifField_Deploy       string = "Deployment(s)"
	NotifField_Anchor       string = "Anchor Worker(s)"
	NotifField_TestE2E      string = "E2E Tests"
	NotifField_TestSmoke    string = "Smoke Tests"
	NotifField_Workflow     string = "Workflow(s)"
)

// Repository
const CommitHashRegex = "[0-9a-f]{40}"
const BuildHashTag = "sha_tag"
const BuildHashLatest = "latest"
const GitHubOrg = "ceramicnetwork"
const ImageVerificationStatusCheck = "ci/image: verify"

const (
	CommitStatus_Failure string = "failure"
	CommitStatus_Pending string = "pending"
	CommitStatus_Success string = "success"
)

// Miscellaneous
const ResourceTag = "Ceramic"
const ServiceName = "cd-manager"
const DefaultCasMaxAnchorWorkers = 1
const DefaultCasMinAnchorWorkers = 0
const DefaultJobStateTtl = 2 * 7 * 24 * time.Hour // Two weeks

// Tests
const (
	Tests_Org      = "3box"
	Tests_Repo     = "ceramic-tests"
	Tests_Ref      = "main"
	Tests_Workflow = "durable.yml"
)

// For CASv5 workers
const CasV5Version = "5"

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
	Clusters map[string]*Cluster `dynamodbav:"clusters,omitempty"`
	Repo     string              `dynamodbav:"repo,omitempty"` // Layout repo
}

type Cluster struct {
	ServiceTasks *TaskSet `dynamodbav:"serviceTasks,omitempty"`
	Tasks        *TaskSet `dynamodbav:"tasks,omitempty"`
	Repo         string   `dynamodbav:"repo,omitempty"` // Cluster repo override
}

type TaskSet struct {
	Tasks map[string]*Task `dynamodbav:"tasks,omitempty"`
	Repo  string           `dynamodbav:"repo,omitempty"` // TaskSet repo override
}

type Task struct {
	Id   string `dynamodbav:"id,omitempty"`
	Repo string `dynamodbav:"repo,omitempty"` // Task repo override
	Temp bool   `dynamodbav:"temp,omitempty"` // Whether the task is meant to go down once it has completed
	Name string `dynamodbav:"name,omitempty"` // Container name
}

// JobSm represents job state machine objects processed by the job manager
type JobSm interface {
	Advance() (job.JobState, error)
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
	QueueJob(job.JobState) error
	QueuedJobs() []job.JobState
	OrderedJobs(job.JobStage) []job.JobState
	AdvanceJob(job.JobState) error
	WriteJob(job.JobState) error
	IterateByType(job.JobType, bool, func(job.JobState) bool) error
	UpdateBuildHash(DeployComponent, string) error
	UpdateDeployHash(DeployComponent, string) error
	GetBuildHashes() (map[DeployComponent]string, error)
	GetDeployHashes() (map[DeployComponent]string, error)
}

// Cache represents an in-memory cache for job states
type Cache interface {
	WriteJob(job.JobState)
	DeleteJob(string)
	JobById(string) (job.JobState, bool)
	JobsByMatcher(func(job.JobState) bool) []job.JobState
}

// Deployment represents a container orchestration service (e.g. AWS ECS)
type Deployment interface {
	LaunchServiceTask(string, string, string, string, map[string]string) (string, error)
	LaunchTask(string, string, string, string, map[string]string) (string, error)
	CheckTask(string, string, bool, bool, ...string) (bool, error)
	GenerateEnvLayout(DeployComponent) (*Layout, error)
	UpdateEnv(*Layout, string) error
	CheckEnv(*Layout) (bool, error)
}

// Notifs represents a notification service (e.g. Discord)
type Notifs interface {
	NotifyJob(...job.JobState)
}

// Manager represents the job manager, which is the central job orchestrator of this service.
type Manager interface {
	NewJob(job.JobState) (string, error)
	CheckJob(string) string
	ProcessJobs(chan bool)
	Pause()
}

// Repository represents a git service hosting our repositories (e.g. GitHub)
type Repository interface {
	GetLatestCommitHash(DeployRepo, string, string) (string, error)
}
