package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"
)

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

func NotifField(job JobType) string {
	switch job {
	case JobType_Deploy:
		return NotifField_Deploy
	case JobType_Anchor:
		return NotifField_Anchor
	case JobType_TestE2E:
		return NotifField_TestE2E
	case JobType_TestSmoke:
		return NotifField_TestSmoke
	default:
		return ""
	}
}

func CeramicEnvPfx() string {
	return "ceramic-" + os.Getenv("ENV")
}

func IsFinishedJob(jobState JobState) bool {
	return (jobState.Stage == JobStage_Skipped) || (jobState.Stage == JobStage_Canceled) || (jobState.Stage == JobStage_Failed) || (jobState.Stage == JobStage_Completed)
}

func IsActiveJob(jobState JobState) bool {
	return (jobState.Stage == JobStage_Started) || (jobState.Stage == JobStage_Waiting)
}

func IsTimedOut(jobState JobState, delay time.Duration) bool {
	// If no timestamp was stored, use the timestamp from the last update.
	startTime := jobState.Ts
	if s, found := jobState.Params[JobParam_Start].(float64); found {
		startTime = time.UnixMilli(int64(s))
	}
	return time.Now().Add(-delay).After(startTime)
}

func IsValidSha(sha string) bool {
	isValidSha, err := regexp.MatchString(CommitHashRegex, sha)
	return err == nil && isValidSha
}

func IsV5WorkerJob(jobState JobState) bool {
	if jobState.Type == JobType_Anchor {
		if version, found := jobState.Params[JobParam_Version].(string); found && (version == CasV5Version) {
			return true
		}
	}
	return false
}
