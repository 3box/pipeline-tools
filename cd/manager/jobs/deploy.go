package jobs

import (
	"fmt"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Job = &deployJob{}

type deployJob struct {
	state manager.JobState
	db    manager.Database
	d     manager.Deployment
	env   string
	sha   string
}

func DeployJob(db manager.Database, d manager.Deployment, jobState manager.JobState) (*deployJob, error) {
	env := os.Getenv("ENV")
	// TODO: Do we need to validate deploy params more thoroughly?
	if component, found := jobState.Params[manager.DeployParam_Component]; !found {
		return nil, fmt.Errorf("deployJob: missing component (ceramic, ipfs, cas)")
	} else if sha, found := jobState.Params[manager.DeployParam_Sha]; !found {
		return nil, fmt.Errorf("deployJob: missing sha")
	} else {
		job := deployJob{jobState, db, d, env, sha.(string)}
		if err := job.populateClusterLayout(component.(string), env); err != nil {
			return nil, err
		}
		return &job, nil
	}
}

func (d deployJob) populateClusterLayout(component, env string) error {
	globalPrefix := "ceramic"
	privateClusterName := manager.GetClusterName(manager.Cluster_Private, env)
	publicClusterName := manager.GetClusterName(manager.Cluster_Public, env)
	casClusterName := manager.GetClusterName(manager.Cluster_Cas, env)
	// Only populate the cluster layout if it wasn't already present. Since the CAS cluster is part of every deployment,
	// use that to detect a job already in progress.
	if _, found := d.state.Params[casClusterName]; !found {
		var privateClusterServices map[string]interface{}
		var publicClusterServices map[string]interface{}
		var casClusterServices map[string]interface{}
		switch component {
		case manager.DeployComponent_Ceramic:
			privateClusterServices = map[string]interface{}{
				privateClusterName + "-" + manager.ServiceSuffix_CeramicNode: nil,
			}
			publicClusterServices = map[string]interface{}{
				publicClusterName + "-" + manager.ServiceSuffix_CeramicNode:    nil,
				publicClusterName + "-" + manager.ServiceSuffix_CeramicGateway: nil,
			}
			if env == manager.EnvType_Prod {
				publicClusterServices[globalPrefix+"-"+manager.ServiceSuffix_Elp11CeramicNode] = nil
				publicClusterServices[globalPrefix+"-"+manager.ServiceSuffix_Elp12CeramicNode] = nil
			}
			casClusterServices = map[string]interface{}{
				casClusterName + "-" + manager.ServiceSuffix_CeramicNode: nil,
			}
		case manager.DeployComponent_Ipfs:
			privateClusterServices = map[string]interface{}{
				privateClusterName + manager.ServiceSuffix_IpfsNode: nil,
			}
			publicClusterServices = map[string]interface{}{
				publicClusterName + "-" + manager.ServiceSuffix_IpfsNode:    nil,
				publicClusterName + "-" + manager.ServiceSuffix_IpfsGateway: nil,
			}
			if env == manager.EnvType_Prod {
				publicClusterServices[globalPrefix+"-"+manager.ServiceSuffix_Elp11IpfsNode] = nil
				publicClusterServices[globalPrefix+"-"+manager.ServiceSuffix_Elp12IpfsNode] = nil
			}
			casClusterServices = map[string]interface{}{
				casClusterName + "-" + manager.ServiceSuffix_IpfsNode: nil,
			}
		case manager.DeployComponent_Cas:
			casClusterServices = map[string]interface{}{
				casClusterName + "-" + manager.ServiceSuffix_CasApi:    nil,
				casClusterName + "-" + manager.ServiceSuffix_CasAnchor: nil,
			}
		default:
			return fmt.Errorf("deployJob: unexpected component: %s", component)
		}
		d.state.Params[privateClusterName] = privateClusterServices
		d.state.Params[publicClusterName] = publicClusterServices
		d.state.Params[casClusterName] = casClusterServices
	}
	return nil
}

func (d deployJob) AdvanceJob() error {
	if d.state.Stage == manager.JobStage_Queued {
		if err := d.updateAllServices(); err != nil {
			d.state.Stage = manager.JobStage_Failed
		} else {
			d.state.Stage = manager.JobStage_Started
		}
	} else if time.Now().Add(-FailureTime).After(d.state.Ts) {
		d.state.Stage = manager.JobStage_Failed
	} else if d.state.Stage == manager.JobStage_Started {
		// Check if all service updates completed
		if running, err := d.checkAllServices(); err != nil {
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
	d.state.Ts = time.Now()
	if err := d.db.UpdateJob(d.state); err != nil {
		return err
	}
	return nil
}

func (d deployJob) updateAllServices() error {
	// TODO: Should we attempt to parallelize these calls?
	if err := d.updateClusterServices(manager.GetClusterName(manager.Cluster_Private, d.env)); err != nil {
		return err
	} else if err = d.updateClusterServices(manager.GetClusterName(manager.Cluster_Public, d.env)); err != nil {
		return err
	} else if err = d.updateClusterServices(manager.GetClusterName(manager.Cluster_Cas, d.env)); err != nil {
		return err
	}
	return nil
}

func (d deployJob) updateClusterServices(cluster string) error {
	services := d.state.Params[cluster].(map[string]interface{})
	for service, _ := range services {
		if id, err := d.d.UpdateService(cluster, service, d.sha); err != nil {
			return err
		} else {
			services[service] = id
		}
	}
	return nil
}

func (d deployJob) checkAllServices() (bool, error) {
	if private, err := d.checkClusterServices(manager.GetClusterName(manager.Cluster_Private, d.env)); err != nil {
		return false, err
	} else if public, err := d.checkClusterServices(manager.GetClusterName(manager.Cluster_Public, d.env)); err != nil {
		return false, err
	} else if cas, err := d.checkClusterServices(manager.GetClusterName(manager.Cluster_Cas, d.env)); err != nil {
		return false, err
	} else if private && public && cas {
		return true, nil
	}
	return false, nil
}

func (d deployJob) checkClusterServices(cluster string) (bool, error) {
	services := d.state.Params[cluster].(map[string]interface{})
	// Check the status of cluster services, only return success if all services were successfully started.
	for service, taskDefId := range services {
		if deployed, err := d.d.CheckService(cluster, service, taskDefId.(string)); err != nil {
			return false, err
		} else if !deployed {
			return false, nil
		}
	}
	return true, nil
}
