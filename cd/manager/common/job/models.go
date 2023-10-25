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

const (
	JobName_Deploy    = "Deployment"
	JobName_Anchor    = "Anchor Worker"
	JobName_TestE2E   = "E2E Tests"
	JobName_TestSmoke = "Smoke Tests"
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
	JobParam_Component string = "component"
	JobParam_Id        string = "id"
	JobParam_Sha       string = "sha"
	JobParam_ShaTag    string = "shaTag"
	JobParam_Error     string = "error"
	JobParam_Layout    string = "layout"
	JobParam_Manual    string = "manual"
	JobParam_Force     string = "force"
	JobParam_Start     string = "start"
	JobParam_Rollback  string = "rollback"
	JobParam_Delayed   string = "delayed"
	JobParam_Stalled   string = "stalled"
	JobParam_Source    string = "source"
	JobParam_Version   string = "version"
	JobParam_Overrides string = "overrides"
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
