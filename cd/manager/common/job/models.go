package job

import "time"

const StageTsIndex = "stage-ts-index"
const TypeTsIndex = "type-ts-index"
const JobTsIndex = "job-ts-index"

type JobType string

// TODO: Clean up smoke/e2e test job types once the new GitHub test workflow is ready
// Ref: https://linear.app/3boxlabs/issue/WS1-1298/clean-up-existing-smokee2e-test-cd-manager-job-types
const (
	JobType_Deploy    JobType = "deploy"
	JobType_Anchor    JobType = "anchor"
	JobType_TestE2E   JobType = "test_e2e"
	JobType_TestSmoke JobType = "test_smoke"
	JobType_Workflow  JobType = "workflow"
)

type JobStage string

const (
	JobStage_Queued    JobStage = "queued"
	JobStage_Dequeued  JobStage = "dequeued"
	JobStage_Skipped   JobStage = "skipped"
	JobStage_Started   JobStage = "started"
	JobStage_Waiting   JobStage = "waiting"
	JobStage_Failed    JobStage = "failed"
	JobStage_Canceled  JobStage = "canceled"
	JobStage_Completed JobStage = "completed"
)

const (
	JobParam_Id     string = "id"
	JobParam_Error  string = "error"
	JobParam_Start  string = "start"
	JobParam_Source string = "source"
)

const (
	DeployJobParam_Component string = "component"
	DeployJobParam_Sha       string = "sha"
	DeployJobParam_ShaTag    string = "shaTag"
	DeployJobParam_DeployTag string = "deployTag"
	DeployJobParam_Layout    string = "layout"
	DeployJobParam_Manual    string = "manual"
	DeployJobParam_Force     string = "force"
	DeployJobParam_Rollback  string = "rollback"
)

const (
	DeployJobTarget_Latest   = "latest"
	DeployJobTarget_Release  = "release"
	DeployJobTarget_Rollback = "rollback"
)

const (
	AnchorJobParam_Delayed   string = "delayed"
	AnchorJobParam_Stalled   string = "stalled"
	AnchorJobParam_Version   string = "version"
	AnchorJobParam_Overrides string = "overrides"
)

const (
	WorkflowJobParam_Name         string = "name"
	WorkflowJobParam_Org          string = "org"
	WorkflowJobParam_Repo         string = "repo"
	WorkflowJobParam_Ref          string = "ref"
	WorkflowJobParam_Workflow     string = "workflow"
	WorkflowJobParam_Inputs       string = "inputs"
	WorkflowJobParam_Environment  string = "environment"
	WorkflowJobParam_Url          string = "url"
	WorkflowJobParam_JobId        string = "job_id"
	WorkflowJobParam_TestSelector string = "test_selector"
)

// JobState represents the state of a job in the database
type JobState struct {
	JobId  string                 `dynamodbav:"job"` // Job ID, same for all stages of an individual Job
	Stage  JobStage               `dynamodbav:"stage"`
	Type   JobType                `dynamodbav:"type"`
	Ts     time.Time              `dynamodbav:"ts"`
	Params map[string]interface{} `dynamodbav:"params"`
	Id     string                 `dynamodbav:"id" json:"-"`           // Globally unique ID for each job update
	Ttl    time.Time              `dynamodbav:"ttl,unixtime" json:"-"` // Record expiration
}

type Workflow struct {
	Org      string
	Repo     string
	Workflow string
	Ref      string
	Inputs   map[string]interface{}
	Url      string
	Id       int64
}
