package jobs

import (
	"fmt"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

const LayoutParam = "layout"

var _ manager.Job = &deployJob{}

type deployJob struct {
	state  manager.JobState
	db     manager.Database
	d      manager.Deployment
	notifs manager.Notifs
	image  string
}

func DeployJob(db manager.Database, d manager.Deployment, notifs manager.Notifs, jobState manager.JobState) (*deployJob, error) {
	if component, found := jobState.Params[manager.EventParam_Component]; !found {
		return nil, fmt.Errorf("deployJob: missing component (ceramic, ipfs, cas)")
	} else if sha, found := jobState.Params[manager.EventParam_Sha]; !found {
		return nil, fmt.Errorf("deployJob: missing sha")
	} else if clusterLayout, err := d.PopulateLayout(component.(string)); err != nil {
		return nil, err
	} else if registryUri, err := d.GetRegistryUri(component.(string)); err != nil {
		return nil, err
	} else {
		// Only overwrite the cluster layout if it wasn't already present.
		if _, found = jobState.Params[LayoutParam]; !found {
			jobState.Params[LayoutParam] = clusterLayout
		}
		return &deployJob{jobState, db, d, notifs, registryUri + ":" + sha.(string)}, nil
	}
}

func (d deployJob) AdvanceJob() error {
	if d.state.Stage == manager.JobStage_Queued {
		if err := d.updateServices(); err != nil {
			d.state.Stage = manager.JobStage_Failed
		} else {
			d.state.Stage = manager.JobStage_Started
		}
	} else if time.Now().Add(-manager.DefaultFailureTime).After(d.state.Ts) {
		d.state.Stage = manager.JobStage_Failed
	} else if d.state.Stage == manager.JobStage_Started {
		// Check if all service updates completed
		if running, err := d.checkServices(); err != nil {
			d.state.Stage = manager.JobStage_Failed
		} else if running {
			d.state.Stage = manager.JobStage_Completed
		} else {
			// Return so we come back again to check
			return nil
		}
	} else {
		// There's nothing left to do so we shouldn't have reached here
		return fmt.Errorf("deployJob: unexpected state: %s", manager.PrintJob(d.state))
	}
	d.notifs.NotifyJob(d.state)
	return d.db.UpdateJob(d.state)
}

func (d deployJob) updateServices() error {
	for cluster, clusterLayout := range d.state.Params[LayoutParam].(map[string]interface{}) {
		for service, _ := range clusterLayout.(map[string]interface{}) {
			if id, err := d.d.UpdateService(cluster, service, d.image); err != nil {
				return err
			} else {
				clusterLayout.(map[string]interface{})[service] = id
			}
		}
	}
	return nil
}

func (d deployJob) checkServices() (bool, error) {
	// Check the status of cluster services, only return success if all services were successfully started.
	for cluster, clusterLayout := range d.state.Params[LayoutParam].(map[string]interface{}) {
		for service, id := range clusterLayout.(map[string]interface{}) {
			if deployed, err := d.d.CheckService(cluster, service, id.(string)); err != nil {
				return false, err
			} else if !deployed {
				return false, nil
			}
		}
	}
	return true, nil
}
