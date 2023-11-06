package manager

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

const commitHashRegex = "[0-9a-f]{40}"
const casV5Version = "5"

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

func ComponentRepo(component DeployComponent) (DeployRepo, error) {
	switch component {
	case DeployComponent_Ceramic:
		return DeployRepo{Org: GitHubOrg_CeramicNetwork, Name: RepoName_Ceramic}, nil
	case DeployComponent_Cas:
		return DeployRepo{Org: GitHubOrg_CeramicNetwork, Name: RepoName_Cas}, nil
	case DeployComponent_CasV5:
		return DeployRepo{Org: GitHubOrg_CeramicNetwork, Name: RepoName_CasV5}, nil
	case DeployComponent_Ipfs:
		return DeployRepo{Org: GitHubOrg_CeramicNetwork, Name: RepoName_Ipfs}, nil
	case DeployComponent_RustCeramic:
		return DeployRepo{Org: GitHubOrg_3Box, Name: RepoName_RustCeramic}, nil
	default:
		return DeployRepo{}, fmt.Errorf("componentRepo: unknown component: %s", component)
	}
}

func IsValidSha(sha string) bool {
	isValidSha, err := regexp.MatchString(commitHashRegex, sha)
	return err == nil && isValidSha
}

func IsV5WorkerJob(jobState job.JobState) bool {
	if jobState.Type == job.JobType_Anchor {
		if version, found := jobState.Params[job.AnchorJobParam_Version].(string); found && (version == casV5Version) {
			return true
		}
	}
	return false
}

// AdvanceJob will move a JobState to a new JobStage in the Database and send an appropriate notification
func AdvanceJob(jobState job.JobState, jobStage job.JobStage, ts time.Time, err error, db Database, notifs Notifs) (job.JobState, error) {
	jobState.Stage = jobStage
	jobState.Ts = ts
	if err != nil {
		jobState.Params[job.JobParam_Error] = err.Error()
	}
	if err = db.AdvanceJob(jobState); err == nil {
		// Only send a notification if the DB update was successful
		notifs.NotifyJob(jobState)
	}
	return jobState, err
}
