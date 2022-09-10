package jobs

import (
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Job = &deployJob{}

type deployJob struct {
	state     manager.JobState
	db        manager.Database
	d         manager.Deployment
	notifs    manager.Notifs
	component manager.DeployComponent
	sha       string
}

func DeployJob(db manager.Database, d manager.Deployment, notifs manager.Notifs, jobState manager.JobState) (*deployJob, error) {
	if component, compFound := jobState.Params[manager.JobParam_Component].(string); !compFound {
		return nil, fmt.Errorf("deployJob: missing component (ceramic, ipfs, cas)")
	} else {
		c := manager.DeployComponent(component)
		sha, shaFound := jobState.Params[manager.JobParam_Sha].(string)
		// If the commit hash was unspecified or invalid, pull the latest build hash from the database.
		if shaFound {
			if isValidSha, err := regexp.MatchString(manager.CommitHashRegex, sha); err != nil {
				return nil, err
			} else if !isValidSha {
				shaFound = false
			}
		}
		if !shaFound {
			if buildHashes, err := db.GetBuildHashes(); err != nil {
				return nil, err
			} else {
				sha = buildHashes[c]
				jobState.Params[manager.JobParam_Sha] = sha
			}
		}
		// Only populate the env layout if it wasn't already present.
		if _, layoutFound := jobState.Params[manager.JobParam_Layout]; !layoutFound {
			if envLayout, err := d.PopulateEnvLayout(c); err != nil {
				return nil, err
			} else {
				jobState.Params[manager.JobParam_Layout] = *envLayout
			}
		}
		return &deployJob{jobState, db, d, notifs, c, sha}, nil
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
	return d.state, d.db.UpdateJob(d.state)
}

func (d deployJob) updateEnv(commitHash string) error {
	if layout, found := d.state.Params[manager.JobParam_Layout].(manager.Layout); found {
		return d.d.UpdateEnv(&layout, commitHash)
	}
	return fmt.Errorf("updateEnv: missing env layout")
}

func (d deployJob) checkEnv() (bool, error) {
	if layout, found := d.state.Params[manager.JobParam_Layout].(manager.Layout); found {
		return d.d.CheckEnv(&layout)
	}
	return false, fmt.Errorf("checkEnv: missing env layout")
}
