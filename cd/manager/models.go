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
	JobStage_Started   JobStage = "started"
	JobStage_Waiting   JobStage = "waiting"
	JobStage_Skipped   JobStage = "skipped"
	JobStage_Failed    JobStage = "failed"
	JobStage_Completed JobStage = "completed"
)

const (
	EnvType_Dev  string = "dev"
	EnvType_Qa   string = "qa"
	EnvType_Tnet string = "tnet"
	EnvType_Prod string = "prod"
)

type Cluster uint8

const (
	Cluster_Private Cluster = iota
	Cluster_Public
	Cluster_Cas
)

const (
	DeployParam_Component string = "component"
	DeployParam_Sha       string = "sha"
)

const (
	DeployComponent_Ceramic string = "ceramic"
	DeployComponent_Ipfs    string = "ipfs"
	DeployComponent_Cas     string = "cas"
)

const (
	ClusterSuffix_Public string = "ex"
	ClusterSuffix_Cas    string = "cas"
)

const (
	ServiceSuffix_CeramicNode      string = "node"
	ServiceSuffix_CeramicGateway   string = "gateway"
	ServiceSuffix_Elp11CeramicNode string = "elp-1-1-node"
	ServiceSuffix_Elp12CeramicNode string = "elp-1-2-node"
	ServiceSuffix_IpfsNode         string = "ipfs-nd"
	ServiceSuffix_IpfsGateway      string = "ipfs-gw"
	ServiceSuffix_Elp11IpfsNode    string = "elp-1-1-ipfs-nd"
	ServiceSuffix_Elp12IpfsNode    string = "elp-1-2-ipfs-nd"
	ServiceSuffix_CasApi           string = "api"
	ServiceSuffix_CasAnchor        string = "anchor"
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

func GetClusterName(cluster Cluster, env string) string {
	globalPrefix := "ceramic"
	switch cluster {
	case Cluster_Private:
		return globalPrefix + "-" + string(env)
	case Cluster_Public:
		return globalPrefix + "-" + string(env) + "-" + ClusterSuffix_Public
	case Cluster_Cas:
		return globalPrefix + "-" + string(env) + "-" + ClusterSuffix_Cas
	}
	return ""
}
