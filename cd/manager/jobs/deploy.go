package jobs

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ manager.Job = &deployJob{}

type deployJob struct {
	baseJob
	component manager.DeployComponent
	sha       string
	manual    bool
	rollback  bool
	d         manager.Deployment
	repo      manager.Repository
}

func DeployJob(jobState job.JobState, db manager.Database, notifs manager.Notifs, d manager.Deployment, repo manager.Repository) (manager.Job, error) {
	if component, found := jobState.Params[job.JobParam_Component].(string); !found {
		return nil, fmt.Errorf("deployJob: missing component (ceramic, ipfs, cas)")
	} else if sha, found := jobState.Params[job.JobParam_Sha].(string); !found {
		return nil, fmt.Errorf("deployJob: missing sha")
	} else {
		manual, _ := jobState.Params[job.JobParam_Manual].(bool)
		rollback, _ := jobState.Params[job.JobParam_Rollback].(bool)
		return &deployJob{baseJob{jobState, db, notifs}, manager.DeployComponent(component), sha, manual, rollback, d, repo}, nil
	}
}

func (d *deployJob) Advance() (job.JobState, error) {
	now := time.Now()
	switch d.state.Stage {
	case job.JobStage_Queued:
		{
			if deployHashes, err := d.db.GetDeployHashes(); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else if err = d.prepareJob(deployHashes); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else if !d.manual && !d.rollback && (d.sha == deployHashes[d.component]) {
				// Skip automated jobs if the commit hash being deployed is the same as the commit hash already
				// deployed. We don't do this for manual jobs because deploying an already deployed hash might be
				// intentional, or for rollbacks because we WANT to redeploy the last successfully deployed hash.
				return d.advance(job.JobStage_Skipped, now, nil)
			} else if envLayout, err := d.d.GenerateEnvLayout(d.component); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else {
				d.state.Params[job.JobParam_Layout] = *envLayout
				// Don't update the timestamp here so that the "dequeued" event remains at the same position on the
				// timeline as the "queued" event.
				return d.advance(job.JobStage_Dequeued, d.state.Ts, nil)
			}
		}
	case job.JobStage_Dequeued:
		{
			if err := d.updateEnv(d.sha); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else {
				d.state.Params[job.JobParam_Start] = time.Now().UnixNano()
				// For started deployments update the build commit hash in the DB.
				if err = d.db.UpdateBuildHash(d.component, d.sha); err != nil {
					// This isn't an error big enough to fail the job, just report and move on.
					log.Printf("deployJob: failed to update build hash: %v, %s", err, manager.PrintJob(d.state))
				}
				return d.advance(job.JobStage_Started, now, nil)
			}
		}
	case job.JobStage_Started:
		{
			if deployed, err := d.checkEnv(); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else if deployed {
				// For completed deployments update the deployed commit hash in the DB.
				if err = d.db.UpdateDeployHash(d.component, d.sha); err != nil {
					// This isn't an error big enough to fail the job, just report and move on.
					log.Printf("deployJob: failed to update deploy hash: %v, %s", err, manager.PrintJob(d.state))
				}
				return d.advance(job.JobStage_Completed, now, nil)
			} else if job.IsTimedOut(d.state, manager.DefaultFailureTime) {
				return d.advance(job.JobStage_Failed, now, manager.Error_CompletionTimeout)
			} else {
				// Return so we come back again to check
				return d.state, nil
			}
		}
	default:
		{
			return d.advance(
				job.JobStage_Failed,
				now,
				fmt.Errorf("deployJob: unexpected state: %s", manager.PrintJob(d.state)),
			)
		}
	}
}

func (d *deployJob) prepareJob(deployHashes map[manager.DeployComponent]string) error {
	if d.rollback {
		// Use the latest successfully deployed commit hash when rolling back
		d.sha = deployHashes[d.component]
	} else
	// - If the specified commit hash is "latest", fetch the latest branch commit hash from GitHub.
	// - Else if it's a valid hash, use it.
	// - Else use the latest build hash from the database.
	//
	// The last two cases will only happen when redeploying manually, so we can note that in the notification.
	if d.sha == manager.BuildHashLatest {
		shaTag, _ := d.state.Params[job.JobParam_ShaTag].(string)
		if latestSha, err := d.repo.GetLatestCommitHash(
			manager.ComponentRepo(d.component),
			manager.EnvBranch(d.component, manager.EnvType(os.Getenv("ENV"))),
			shaTag,
		); err != nil {
			return err
		} else {
			d.sha = latestSha
		}
	} else {
		if !manager.IsValidSha(d.sha) {
			// Get the latest build commit hash from the database when making a fresh deployment
			if buildHashes, err := d.db.GetBuildHashes(); err != nil {
				return err
			} else {
				d.sha = buildHashes[d.component]
			}
		}
		d.manual = true
	}
	d.state.Params[job.JobParam_Sha] = d.sha
	if d.manual {
		d.state.Params[job.JobParam_Manual] = true
	}
	return nil
}

func (d *deployJob) updateEnv(commitHash string) error {
	if layout, found := d.state.Params[job.JobParam_Layout].(manager.Layout); found {
		return d.d.UpdateEnv(&layout, commitHash)
	}
	return fmt.Errorf("updateEnv: missing env layout")
}

func (d *deployJob) checkEnv() (bool, error) {
	if layout, found := d.state.Params[job.JobParam_Layout].(manager.Layout); !found {
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
		return false, err
	} else {
		return d.d.CheckEnv(ceramicLayout)
	}
}
