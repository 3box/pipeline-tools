package jobs

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/exp/slices"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ manager.JobSm = &deployJob{}

type deployJob struct {
	baseJob
	component manager.DeployComponent
	sha       string
	shaTag    string
	deployTag string
	manual    bool
	rollback  bool
	force     bool
	env       string
	d         manager.Deployment
	repo      manager.Repository
}

const (
	envBranch_Dev  string = "develop"
	envBranch_Qa   string = "qa"
	envBranch_Tnet string = "release-candidate"
	envBranch_Prod string = "main"
)

const (
	serviceSuffix_CeramicNode  string = "node"
	serviceSuffix_IpfsNode     string = "ipfs-nd"
	serviceSuffix_CasApi       string = "api"
	serviceSuffix_CasWorker    string = "anchor"
	serviceSuffix_CasScheduler string = "scheduler"
	serviceSuffix_Elp          string = "elp"
)

const (
	containerName_CeramicNode    string = "ceramic_node"
	containerName_IpfsNode       string = "go-ipfs"
	containerName_CasApi         string = "cas_api"
	containerName_CasWorker      string = "cas_anchor"
	containerName_CasV5Scheduler string = "scheduler"
	containerName_RustCeramic    string = "rust-ceramic"
)

const defaultFailureTime = 30 * time.Minute
const anchorWorkerRepo = "ceramic-prod-cas-runner"

func DeployJob(jobState job.JobState, db manager.Database, notifs manager.Notifs, d manager.Deployment, repo manager.Repository) (manager.JobSm, error) {
	if component, found := jobState.Params[job.DeployJobParam_Component].(string); !found {
		return nil, fmt.Errorf("deployJob: missing component (ceramic, ipfs, cas, casv5, rust-ceramic)")
	} else if sha, found := jobState.Params[job.DeployJobParam_Sha].(string); !found {
		return nil, fmt.Errorf("deployJob: missing target")
	} else if shaTag, found := jobState.Params[job.DeployJobParam_ShaTag].(string); !found {
		return nil, fmt.Errorf("deployJob: missing tag")
	} else {
		deployTag, _ := jobState.Params[job.DeployJobParam_DeployTag].(string)
		manual, _ := jobState.Params[job.DeployJobParam_Manual].(bool)
		rollback, _ := jobState.Params[job.DeployJobParam_Rollback].(bool)
		force, _ := jobState.Params[job.DeployJobParam_Force].(bool)
		return &deployJob{baseJob{jobState, db, notifs}, manager.DeployComponent(component), sha, shaTag, deployTag, manual, rollback, force, os.Getenv(manager.EnvVar_Env), d, repo}, nil
	}
}

func (d deployJob) Advance() (job.JobState, error) {
	now := time.Now()
	switch d.state.Stage {
	case job.JobStage_Queued:
		{
			if deployTags, err := d.db.GetDeployTags(); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else if err = d.prepareJob(deployTags); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else if deployTag, found := d.state.Params[job.DeployJobParam_DeployTag].(string); found &&
				!d.manual && !d.force &&
				(deployTag == strings.Split(deployTags[d.component], ",")[0]) {
				// Skip automated jobs if the tag being deployed is the same as the tag already deployed. We don't do
				// this for manual jobs because deploying an already deployed tag might be intentional, or for force
				// deploys/rollbacks because we WANT to push through such deployments.
				//
				// Rollbacks are also force deploys, so we don't need to check for the former explicitly since we're
				// already checking for force deploys.
				return d.advance(job.JobStage_Skipped, now, nil)
			} else if envLayout, err := d.generateEnvLayout(d.component); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else {
				d.state.Params[job.DeployJobParam_Layout] = *envLayout
				// Advance the timestamp by a tiny amount so that the "dequeued" event remains at the same position on
				// the timeline as the "queued" event but still ahead of it.
				return d.advance(job.JobStage_Dequeued, d.state.Ts.Add(time.Nanosecond), nil)
			}
		}
	case job.JobStage_Dequeued:
		{
			if err := d.updateEnv(); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else {
				d.state.Params[job.JobParam_Start] = float64(time.Now().UnixNano())
				// For started deployments update the build tag in the DB
				if err = d.db.UpdateBuildTag(d.component, d.deployTag); err != nil {
					// This isn't an error big enough to fail the job, just report and move on.
					log.Printf("deployJob: failed to update build tag: %v, %s", err, manager.PrintJob(d.state))
				}
				return d.advance(job.JobStage_Started, now, nil)
			}
		}
	case job.JobStage_Started:
		{
			if deployed, err := d.checkEnv(); err != nil {
				return d.advance(job.JobStage_Failed, now, err)
			} else if deployed {
				// For completed deployments update the deployed tag in the DB, and append the deployment target.
				if err = d.db.UpdateDeployTag(d.component, d.deployTag+","+d.sha); err != nil {
					// This isn't an error big enough to fail the job, just report and move on.
					log.Printf("deployJob: failed to update deploy tag: %v, %s", err, manager.PrintJob(d.state))
				}
				return d.advance(job.JobStage_Completed, now, nil)
			} else if job.IsTimedOut(d.state, defaultFailureTime) {
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

func (d deployJob) prepareJob(deployTags map[manager.DeployComponent]string) error {
	deployTag := ""
	manual := false
	if d.rollback {
		// Use the latest successfully deployed tag when rolling back
		deployTag = deployTags[d.component]
	} else
	// - If the specified deployment target is "latest", fetch the latest branch commit hash from GitHub.
	// - Else if the specified deployment target is "release", use the specified release tag.
	// - Else if it's a valid hash, use it.
	// - Else use the last successfully deployed target from the database.
	//
	// The last 3 cases will only happen when (re)deploying manually, so we can note that in the notification.
	if d.sha == job.DeployJobTarget_Latest {
		if repo, err := manager.ComponentRepo(d.component); err != nil {
			return err
		} else if latestSha, err := d.repo.GetLatestCommitHash(
			repo.Org,
			repo.Name,
			d.envBranch(d.component, manager.EnvType(os.Getenv(manager.EnvVar_Env))),
			d.shaTag,
		); err != nil {
			return err
		} else {
			deployTag = latestSha
		}
	} else if (d.sha == job.DeployJobTarget_Release) || (d.sha == job.DeployJobTarget_Rollback) {
		deployTag = d.shaTag
		manual = true
	} else if manager.IsValidSha(d.sha) {
		deployTag = d.sha
		manual = true
	} else {
		return fmt.Errorf("prepareJob: invalid deployment type")
	}
	d.state.Params[job.DeployJobParam_DeployTag] = deployTag
	if manual {
		d.state.Params[job.DeployJobParam_Manual] = true
	}
	return nil
}

func (d deployJob) updateEnv() error {
	// Layout should already be present
	layout, _ := d.state.Params[job.DeployJobParam_Layout].(manager.Layout)
	return d.d.UpdateLayout(&layout, d.deployTag)
}

func (d deployJob) checkEnv() (bool, error) {
	// Layout should already be present
	layout, _ := d.state.Params[job.DeployJobParam_Layout].(manager.Layout)
	if deployed, err := d.d.CheckLayout(&layout); err != nil {
		return false, err
	} else if !deployed || ((d.component != manager.DeployComponent_Ipfs) && (d.component != manager.DeployComponent_RustCeramic)) {
		return deployed, nil
	} else
	// Make sure that after IPFS or rust-ceramic is deployed, we find Ceramic tasks that have been stable for a few
	// minutes before marking the job complete.
	//
	// In this case, we want to check whether *some* version of Ceramic is stable and not any specific version, like we
	// normally do when checking for successful deployments, so it's OK to rebuild the Ceramic layout on-the-fly each
	// time instead of storing it in the database.
	if ceramicLayout, err := d.generateEnvLayout(manager.DeployComponent_Ceramic); err != nil {
		return false, err
	} else {
		return d.d.CheckLayout(ceramicLayout)
	}
}

func (d deployJob) generateEnvLayout(component manager.DeployComponent) (*manager.Layout, error) {
	privateCluster := "ceramic-" + d.env
	publicCluster := "ceramic-" + d.env + "-ex"
	casCluster := "ceramic-" + d.env + "-cas"
	casV5Cluster := "app-cas-" + d.env
	clusters := []string{privateCluster, publicCluster, casCluster, casV5Cluster}
	if ecrRepo, err := d.componentEcrRepo(component); err != nil {
		return nil, err
	} else
	// Populate the service layout by retrieving the clusters/services from ECS
	if currentLayout, err := d.d.GetLayout(clusters); err != nil {
		return nil, err
	} else {
		newLayout := &manager.Layout{Clusters: map[string]*manager.Cluster{}, Repo: &ecrRepo}
		for cluster, clusterLayout := range currentLayout.Clusters {
			for service, task := range clusterLayout.ServiceTasks.Tasks {
				if newTask := d.componentTask(component, cluster, service, strings.Split(task.Name, ",")); newTask != nil {
					if newLayout.Clusters[cluster] == nil {
						// We found at least one matching task, so we can start populating the cluster layout.
						newLayout.Clusters[cluster] = &manager.Cluster{ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{}}}
					}
					// Set the task definition to the one currently running. For most cases, this will be overwritten by
					// a new definition, but for some cases, we might want to use a layout with currently running
					// definitions and not updated ones, e.g. to check if an existing deployment is stable.
					newTask.Id = task.Id
					newLayout.Clusters[cluster].ServiceTasks.Tasks[service] = newTask
				}
			}
		}
		// If CAS is bing deployed, add the Anchor Worker to the layout since it doesn't get updated through an ECS
		// service.
		if component == manager.DeployComponent_Cas {
			newLayout.Clusters[casCluster].Tasks = &manager.TaskSet{Tasks: map[string]*manager.Task{
				casCluster + "-" + serviceSuffix_CasWorker: {
					Repo: &manager.Repo{Name: anchorWorkerRepo},
					Temp: true, // Anchor workers do not stay up permanently
					Name: containerName_CasWorker,
				},
			}}
		}
		return newLayout, nil
	}
}

func (d deployJob) componentTask(component manager.DeployComponent, cluster, service string, containerNames []string) *manager.Task {
	// Skip any ELP services (e.g. "ceramic-elp-1-1-node")
	serviceNameParts := strings.Split(service, "-")
	if (len(serviceNameParts) >= 2) && (serviceNameParts[1] == serviceSuffix_Elp) {
		return nil
	}
	switch component {
	case manager.DeployComponent_Ceramic:
		// All clusters have Ceramic nodes
		if strings.Contains(service, serviceSuffix_CeramicNode) {
			return &manager.Task{Name: containerName_CeramicNode}
		}
	case manager.DeployComponent_Ipfs:
		if strings.Contains(service, serviceSuffix_IpfsNode) && slices.Contains(containerNames, containerName_IpfsNode) {
			return &manager.Task{Name: containerName_IpfsNode}
		}
	case manager.DeployComponent_Cas:
		if (cluster == "ceramic-"+d.env+"-cas") && strings.Contains(service, serviceSuffix_CasApi) {
			return &manager.Task{Name: containerName_CasApi}
		}
	case manager.DeployComponent_CasV5:
		if (cluster == "app-cas-"+d.env) && strings.Contains(service, serviceSuffix_CasScheduler) {
			return &manager.Task{Name: containerName_CasV5Scheduler}
		}
	case manager.DeployComponent_RustCeramic:
		if strings.Contains(service, serviceSuffix_IpfsNode) && slices.Contains(containerNames, containerName_RustCeramic) {
			return &manager.Task{Name: containerName_RustCeramic}
		}
	default:
		log.Printf("componentTask: unknown component: %s", component)
	}
	return nil
}

func (d deployJob) componentEcrRepo(component manager.DeployComponent) (manager.Repo, error) {
	switch component {
	case manager.DeployComponent_Ceramic:
		return manager.Repo{Name: "ceramic-prod"}, nil
	case manager.DeployComponent_Ipfs:
		return manager.Repo{Name: "go-ipfs-prod"}, nil
	case manager.DeployComponent_Cas:
		return manager.Repo{Name: "ceramic-prod-cas"}, nil
	case manager.DeployComponent_CasV5:
		return manager.Repo{Name: "app-cas-scheduler"}, nil
	case manager.DeployComponent_RustCeramic:
		return manager.Repo{Name: "ceramic-one", Public: true}, nil
	default:
		return manager.Repo{}, fmt.Errorf("componentEcrRepo: unknown component: %s", component)
	}
}

func (d deployJob) envBranch(component manager.DeployComponent, env manager.EnvType) string {
	// All rust-ceramic deploys are currently from the "main" branch
	if component == manager.DeployComponent_RustCeramic {
		return envBranch_Prod
	}
	switch env {
	case manager.EnvType_Dev:
		return envBranch_Dev
	case manager.EnvType_Qa:
		// Ceramic and CAS "qa" deploys correspond to the "develop" branch
		switch component {
		case manager.DeployComponent_Ceramic:
			return envBranch_Dev
		case manager.DeployComponent_Cas:
			return envBranch_Dev
		case manager.DeployComponent_CasV5:
			return envBranch_Dev
		default:
			return envBranch_Qa
		}
	case manager.EnvType_Tnet:
		return envBranch_Tnet
	case manager.EnvType_Prod:
		return envBranch_Prod
	default:
		return ""
	}
}
