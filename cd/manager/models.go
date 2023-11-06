package manager

import (
	"fmt"
	"time"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

const DefaultTick = 10 * time.Second
const DefaultTtlDays = 1
const DefaultHttpWaitTime = 30 * time.Second
const DefaultWaitTime = 5 * time.Minute

type EnvType string

const (
	EnvType_Dev  EnvType = "dev"
	EnvType_Qa   EnvType = "qa"
	EnvType_Tnet EnvType = "tnet"
	EnvType_Prod EnvType = "prod"
)

type DeployComponent string

const (
	DeployComponent_Ceramic     DeployComponent = "ceramic"
	DeployComponent_Cas         DeployComponent = "cas"
	DeployComponent_CasV5       DeployComponent = "casv5"
	DeployComponent_Ipfs        DeployComponent = "ipfs"
	DeployComponent_RustCeramic DeployComponent = "rust-ceramic"
)

type DeployRepo struct {
	Org  string
	Name string
}

const (
	RepoName_Ceramic     = "js-ceramic"
	RepoName_Cas         = "ceramic-anchor-service"
	RepoName_CasV5       = "go-cas"
	RepoName_Ipfs        = "go-ipfs-daemon"
	RepoName_RustCeramic = "rust-ceramic"
)

const (
	GitHubOrg_CeramicNetwork = "ceramicnetwork"
	GitHubOrg_3Box           = "3box"
)

var (
	Error_StartupTimeout    = fmt.Errorf("startup timeout")
	Error_CompletionTimeout = fmt.Errorf("completion timeout")
)

const (
	EnvVar_Env = "ENV"
)

type WorkflowStatus uint8

const (
	WorkflowStatus_InProgress WorkflowStatus = iota
	WorkflowStatus_Canceled
	WorkflowStatus_Failure
	WorkflowStatus_Success
)

const ServiceName = "cd-manager"

// Layout (as well as Cluster, Repo, TaskSet, and Task) are a generic representation of our service structure within
// an orchestration service (e.g. AWS ECS).
type Layout struct {
	Clusters map[string]*Cluster `dynamodbav:"clusters,omitempty"`
	Repo     *Repo               `dynamodbav:"repo,omitempty"` // Layout repo
}

type Repo struct {
	Name   string `dynamodbav:"name,omitempty"`
	Public bool   `dynamodbav:"public,omitempty"`
}

type Cluster struct {
	ServiceTasks *TaskSet `dynamodbav:"serviceTasks,omitempty"`
	Tasks        *TaskSet `dynamodbav:"tasks,omitempty"`
	Repo         *Repo    `dynamodbav:"repo,omitempty"` // Cluster repo override
}

type TaskSet struct {
	Tasks map[string]*Task `dynamodbav:"tasks,omitempty"`
	Repo  *Repo            `dynamodbav:"repo,omitempty"` // TaskSet repo override
}

type Task struct {
	Id   string `dynamodbav:"id,omitempty"`
	Repo *Repo  `dynamodbav:"repo,omitempty"` // Task repo override
	Temp bool   `dynamodbav:"temp,omitempty"` // Whether the task is meant to go down once it has completed
	Name string `dynamodbav:"name,omitempty"` // Container name
}

type Workflow struct {
	Org      string
	Repo     string
	Workflow string
	Ref      string
	Inputs   map[string]interface{}
}

// JobSm represents job state machine objects processed by the job manager
type JobSm interface {
	Advance() (job.JobState, error)
}

// ApiGw represents an API Gateway service containing APIs we wish to invoke directly, i.e. not through an API call
// (e.g. AWS API Gateway).
type ApiGw interface {
	Invoke(method, resourceId, restApiId, pathWithQueryString string) (string, error)
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
	UpdateBuildTag(DeployComponent, string) error
	UpdateDeployTag(DeployComponent, string) error
	GetBuildTags() (map[DeployComponent]string, error)
	GetDeployTags() (map[DeployComponent]string, error)
}

// Cache represents an in-memory cache for job states
type Cache interface {
	WriteJob(job.JobState)
	DeleteJob(jobId string)
	JobById(jobId string) (job.JobState, bool)
	JobsByMatcher(func(job.JobState) bool) []job.JobState
}

// Deployment represents a container orchestration service (e.g. AWS ECS)
type Deployment interface {
	LaunchServiceTask(cluster, service, family, container string, overrides map[string]string) (string, error)
	LaunchTask(cluster, family, container, vpcConfigParam string, overrides map[string]string) (string, error)
	CheckTask(cluster, taskDefId string, running, stable bool, taskIds ...string) (bool, error)
	GetLayout(clusters []string) (*Layout, error)
	UpdateLayout(*Layout, string) error
	CheckLayout(*Layout) (bool, error)
}

// Notifs represents a notification service (e.g. Discord)
type Notifs interface {
	NotifyJob(...job.JobState)
}

// Manager represents the job manager, which is the central job orchestrator of this service.
type Manager interface {
	NewJob(job.JobState) (job.JobState, error)
	CheckJob(jobId string) job.JobState
	ProcessJobs(shutdownCh chan bool)
	Pause()
}

// Repository represents a git service hosting our repositories (e.g. GitHub)
type Repository interface {
	GetLatestCommitHash(org, repo, branch, shaTag string) (string, error)
	StartWorkflow(Workflow) error
	FindMatchingWorkflowRun(workflow Workflow, jobId string, searchTime time.Time) (int64, string, error)
	CheckWorkflowStatus(workflow Workflow, workflowRunId int64) (WorkflowStatus, error)
}
