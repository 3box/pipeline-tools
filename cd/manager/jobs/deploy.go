package jobs

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Job = &deployJob{}

type deployJob struct {
	state     manager.JobState
	db        manager.Database
	d         manager.Deployment
	repo      manager.Repository
	notifs    manager.Notifs
	component manager.DeployComponent
	sha       string
}

func DeployJob(db manager.Database, d manager.Deployment, repo manager.Repository, notifs manager.Notifs, jobState manager.JobState) (manager.Job, error) {
	if component, compFound := jobState.Params[manager.JobParam_Component].(string); !compFound {
		return nil, fmt.Errorf("deployJob: missing component (ceramic, ipfs, cas)")
	} else if sha, shaFound := jobState.Params[manager.JobParam_Sha].(string); !shaFound {
		return nil, fmt.Errorf("deployJob: missing sha")
	} else {
		c := manager.DeployComponent(component)
		// If "layout" is absent, this job has been dequeued for the first time and we need to do some preprocessing.
		if _, found := jobState.Params[manager.JobParam_Layout]; !found {
			// - If the specified commit hash is "latest", fetch the latest branch commit hash from GitHub.
			// - Else if it's a valid hash, use it.
			// - Else use the latest build hash from the database.
			//
			// The last two cases will only happen when redeploying manually, so we can note that in the notification.
			manual := true
			if sha == manager.BuildHashLatest {
				if latestSha, err := repo.GetLatestCommitHash(
					manager.ComponentRepo(c),
					manager.EnvBranch(manager.EnvType(os.Getenv("ENV"))),
				); err != nil {
					return nil, err
				} else {
					sha = latestSha
					manual = false
				}
			} else if isValidSha, err := regexp.MatchString(manager.CommitHashRegex, sha); (err != nil) || !isValidSha {
				if buildHashes, err := db.GetBuildHashes(); err != nil {
					return nil, err
				} else {
					sha = buildHashes[c]
				}
			}
			jobState.Params[manager.JobParam_Sha] = sha
			if manual {
				jobState.Params[manager.JobParam_Manual] = true
			}
			if envLayout, err := d.GenerateEnvLayout(c); err != nil {
				return nil, err
			} else {
				jobState.Params[manager.JobParam_Layout] = *envLayout
			}
			if err := db.WriteJob(jobState); err != nil {
				return nil, err
			}
			// Send notification for job dequeued for the first time
			notifs.NotifyJob(jobState)
		}
		return &deployJob{jobState, db, d, repo, notifs, c, sha}, nil
	}
}

func (d deployJob) AdvanceJob() (manager.JobState, error) {
	if d.state.Stage == manager.JobStage_Queued {
		if err := d.updateEnv(d.sha); err != nil {
			d.state.Stage = manager.JobStage_Failed
			d.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("deployJob: error updating service: %v, %s", err, manager.PrintJob(d.state))
		} else {
			d.state.Stage = manager.JobStage_Started
			// For started deployments update the build commit hash in the DB.
			if err = d.db.UpdateBuildHash(d.component, d.sha); err != nil {
				// This isn't an error big enough to fail the job, just report and move on.
				log.Printf("deployJob: failed to update build hash: %v, %s", err, manager.PrintJob(d.state))
			}
		}
	} else if time.Now().Add(-manager.DefaultFailureTime).After(d.state.Ts) {
		d.state.Stage = manager.JobStage_Failed
		d.state.Params[manager.JobParam_Error] = manager.Error_Timeout
		log.Printf("deployJob: job timed out: %s", manager.PrintJob(d.state))
	} else if d.state.Stage == manager.JobStage_Started {
		// Check if all service updates completed
		if running, err := d.checkEnv(); err != nil {
			d.state.Stage = manager.JobStage_Failed
			d.state.Params[manager.JobParam_Error] = err.Error()
			log.Printf("deployJob: error checking services running status: %v, %s", err, manager.PrintJob(d.state))
		} else if running {
			d.state.Stage = manager.JobStage_Completed
			// For completed deployments update the deploy commit hash in the DB.
			if err = d.db.UpdateDeployHash(d.component, d.sha); err != nil {
				// This isn't an error big enough to fail the job, just report and move on.
				log.Printf("deployJob: failed to update deploy hash: %v, %s", err, manager.PrintJob(d.state))
			}
		} else {
			// Return so we come back again to check
			return d.state, nil
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return d.state, fmt.Errorf("deployJob: unexpected state: %s", manager.PrintJob(d.state))
	}
	// Only send started/completed/failed notifications.
	if (d.state.Stage == manager.JobStage_Started) || (d.state.Stage == manager.JobStage_Failed) || (d.state.Stage == manager.JobStage_Completed) {
		d.notifs.NotifyJob(d.state)
	}
	return d.state, d.db.AdvanceJob(d.state)
}

func (d deployJob) updateEnv(commitHash string) error {
	if layout, found := d.state.Params[manager.JobParam_Layout].(manager.Layout); found {
		return d.d.UpdateEnv(&layout, commitHash)
	}
	return fmt.Errorf("updateEnv: missing env layout")
}

func (d deployJob) checkEnv() (bool, error) {
	if layout, found := d.state.Params[manager.JobParam_Layout].(manager.Layout); !found {
		return false, fmt.Errorf("checkEnv: missing env layout")
	} else if deployed, err := d.d.CheckEnv(&layout); err != nil {
		return false, err
	} else if !deployed || (d.component != manager.DeployComponent_Ipfs) {
		return deployed, nil
	} else
	// Make sure that after IPFS is deployed, we find Ceramic tasks that have been stable for a few minutes before
	// marking the job complete.
	//
	// In this case, we want to check whether *some* version of Ceramic is stable and not any specific version, like we
	// normally do when checking for successful deployments, so it's OK to rebuild the Ceramic layout on-the-fly each
	// time instead of storing it in the database.
	if ceramicLayout, err := d.d.GenerateEnvLayout(manager.DeployComponent_Ceramic); err != nil {
		log.Printf("checkEnv: ceramic layout generation failed: %v, %s", err, manager.PrintJob(d.state))
		return false, err
	} else {
		return d.d.CheckEnv(ceramicLayout)
	}
}
