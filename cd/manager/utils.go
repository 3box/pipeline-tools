package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

func PrintJob(jobStates ...job.JobState) string {
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

func EnvBranch(component DeployComponent, env EnvType) string {
	switch env {
	case EnvType_Dev:
		return EnvBranch_Dev
	case EnvType_Qa:
		// Ceramic and CAS "qa" deploys correspond to the "develop" branch
		switch component {
		case DeployComponent_Ceramic:
			return EnvBranch_Dev
		case DeployComponent_Cas:
			return EnvBranch_Dev
		case DeployComponent_CasV5:
			return EnvBranch_Dev
		default:
			return EnvBranch_Qa
		}
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
	case DeployComponent_CasV5:
		return DeployRepo_CasV5
	case DeployComponent_Ipfs:
		return DeployRepo_Ipfs
	default:
		return ""
	}
}

func NotifField(jt job.JobType) string {
	switch jt {
	case job.JobType_Deploy:
		return NotifField_Deploy
	case job.JobType_Anchor:
		return NotifField_Anchor
	case job.JobType_TestE2E:
		return NotifField_TestE2E
	case job.JobType_TestSmoke:
		return NotifField_TestSmoke
	default:
		return ""
	}
}

func CeramicEnvPfx() string {
	return "ceramic-" + os.Getenv("ENV")
}

func IsValidSha(sha string) bool {
	isValidSha, err := regexp.MatchString(CommitHashRegex, sha)
	return err == nil && isValidSha
}

func IsV5WorkerJob(jobState job.JobState) bool {
	if jobState.Type == job.JobType_Anchor {
		if version, found := jobState.Params[job.JobParam_Version].(string); found && (version == CasV5Version) {
			return true
		}
	}
	return false
}

// AdvanceJob will move a JobState to a new JobStage in the Database and send an appropriate notification
func AdvanceJob(jobState *job.JobState, jobStage job.JobStage, ts time.Time, err error, db Database, notifs Notifs) (job.JobState, error) {
	jobState.Stage = jobStage
	jobState.Ts = ts
	if err != nil {
		jobState.Params[job.JobParam_Error] = err.Error()
	}
	if err = db.AdvanceJob(*jobState); err != nil {
		notifs.NotifyJob(*jobState)
	}
	return *jobState, err
}
