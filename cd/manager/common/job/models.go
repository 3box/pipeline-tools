package job

import "time"

const StageTsIndex = "stage-ts-index"
const TypeTsIndex = "type-ts-index"
const JobTsIndex = "job-ts-index"

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
	DeployJobParam_Layout    string = "layout"
	DeployJobParam_Manual    string = "manual"
	DeployJobParam_Force     string = "force"
	DeployJobParam_Rollback  string = "rollback"
)

const (
	AnchorJobParam_Delayed   string = "delayed"
	AnchorJobParam_Stalled   string = "stalled"
	AnchorJobParam_Version   string = "version"
	AnchorJobParam_Overrides string = "overrides"
)

// JobState represents the state of a job in the database
type JobState struct {
	Job    string                 `dynamodbav:"job"` // Job ID, same for all stages of an individual Job
	Stage  JobStage               `dynamodbav:"stage"`
	Type   JobType                `dynamodbav:"type"`
	Ts     time.Time              `dynamodbav:"ts"`
	Params map[string]interface{} `dynamodbav:"params"`
	Id     string                 `dynamodbav:"id" json:"-"`           // Globally unique ID for each job update
	Ttl    time.Time              `dynamodbav:"ttl,unixtime" json:"-"` // Record expiration
}
