package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Deployment = &Ecs{}

type Ecs struct {
	ecsClient *ecs.Client
	ssmClient *ssm.Client
	env       manager.EnvType
	ecrUri    string
}

type ecsFailure struct {
	arn, detail, reason string
}

func NewEcs(cfg aws.Config) manager.Deployment {
	ecrUri := os.Getenv("AWS_ACCOUNT_ID") + ".dkr.ecr." + os.Getenv("AWS_REGION") + ".amazonaws.com/"
	return &Ecs{ecs.NewFromConfig(cfg), ssm.NewFromConfig(cfg), manager.EnvType(os.Getenv("ENV")), ecrUri}
}

func (e Ecs) LaunchServiceTask(cluster, service, family, container string, overrides map[string]string) (string, error) {
	if output, err := e.describeEcsService(cluster, service); err != nil {
		return "", err
	} else {
		return e.runEcsTask(cluster, family, container, output.Services[0].NetworkConfiguration, overrides)
	}
}

func (e Ecs) LaunchTask(cluster, family, container, vpcConfigParam string, overrides map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// Get the VPC configuration from SSM
	input := &ssm.GetParameterInput{
		Name:           aws.String(vpcConfigParam),
		WithDecryption: false,
	}
	output, err := e.ssmClient.GetParameter(ctx, input)
	if err != nil {
		log.Printf("launchTask: get vpc config error: %s, %s, %s, %+v, %v", cluster, family, vpcConfigParam, overrides, err)
		return "", err
	}
	var vpcConfig types.AwsVpcConfiguration
	if err = json.Unmarshal([]byte(*output.Parameter.Value), &vpcConfig); err != nil {
		log.Printf("launchTask: error unmarshaling worker network configuration: %s, %s, %s, %+v, %v", cluster, family, vpcConfigParam, overrides, err)
		return "", err
	}
	return e.runEcsTask(cluster, family, container, &types.NetworkConfiguration{AwsvpcConfiguration: &vpcConfig}, overrides)
}

func (e Ecs) CheckTask(cluster, taskDefArn string, running, stable bool, taskArns ...string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// Describe cluster tasks matching the specified ARNs
	input := &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   taskArns,
	}
	output, err := e.ecsClient.DescribeTasks(ctx, input)
	if err != nil {
		log.Printf("checkTask: describe service error: %s, %s, %v", cluster, taskArns, err)
		return false, err
	}
	var checkStatus types.DesiredStatus
	if running {
		checkStatus = types.DesiredStatusRunning
	} else {
		checkStatus = types.DesiredStatusStopped
	}
	// If checking for running tasks, at least one task must be present, but when checking for stopped tasks, it's ok to
	// have found no matching tasks (i.e. the tasks have already stopped and been removed from the list).
	tasksFound := !running
	tasksInState := true
	if len(output.Tasks) > 0 {
		// We found one or more tasks, only return true if all specified tasks were in the right state for at least a
		// few minutes.
		for _, task := range output.Tasks {
			// If a task definition ARN was specified, make sure that we found at least one task with that definition.
			if (len(taskDefArn) == 0) || (*task.TaskDefinitionArn == taskDefArn) {
				tasksFound = true
				if (*task.LastStatus != string(checkStatus)) ||
					(running && stable && time.Now().Add(-manager.DefaultWaitTime).Before(*task.StartedAt)) {
					tasksInState = false
				}
			}
		}
	}
	return tasksFound && tasksInState, nil
}

func (e Ecs) GenerateEnvLayout(component manager.DeployComponent) (*manager.Layout, error) {
	privateCluster := manager.CeramicEnvPfx()
	publicCluster := manager.CeramicEnvPfx() + "-ex"
	casCluster := manager.CeramicEnvPfx() + "-cas"
	casV5Cluster := "app-cas-" + string(e.env)
	ecrRepo, err := e.componentEcrRepo(component)
	if err != nil {
		log.Printf("generateEnvLayout: ecr repo error: %s, %v", component, err)
		return nil, err
	}
	// Populate the service layout by retrieving the clusters/services from ECS
	layout := &manager.Layout{Clusters: map[string]*manager.Cluster{}, Repo: ecrRepo}
	casSchedulerFound := false
	for _, cluster := range []string{privateCluster, publicCluster, casCluster, casV5Cluster} {
		if clusterServices, err := e.listEcsServices(cluster); err != nil {
			log.Printf("generateEnvLayout: list services error: %s, %v", cluster, err)
			return nil, err
		} else {
			for _, serviceArn := range clusterServices.ServiceArns {
				service := e.serviceNameFromArn(serviceArn)
				if task, matched := e.componentTask(component, cluster, service); matched {
					if _, found := layout.Clusters[cluster]; !found {
						// We found at least one matching task, so we can start populating the cluster layout.
						layout.Clusters[cluster] = &manager.Cluster{ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{}}}
					}
					descSvcOutput, err := e.describeEcsService(cluster, service)
					if err != nil {
						log.Printf("generateEnvLayout: describe service error: %s, %s, %v", cluster, service, err)
						return nil, err
					}
					// Set the task definition to the one currently running. For most cases, this will be overwritten by
					// a new definition, but for some cases, we might want to use a layout with currently running
					// definitions and not updated ones, e.g. to check if an existing deployment is stable.
					task.Id = *descSvcOutput.Services[0].TaskDefinition
					layout.Clusters[cluster].ServiceTasks.Tasks[service] = task
					casSchedulerFound = casSchedulerFound ||
						((component == manager.DeployComponent_Cas) && strings.Contains(service, manager.ServiceSuffix_CasScheduler))
				}
			}
		}
	}
	// If the CAS Scheduler service was present, add the CASv2 worker to the layout since it doesn't get updated through
	// an ECS Service.
	if casSchedulerFound {
		layout.Clusters[casCluster].Tasks = &manager.TaskSet{Tasks: map[string]*manager.Task{
			casCluster + "-" + manager.ServiceSuffix_CasWorker: {
				Repo: manager.CeramicEnvPfx() + "-cas-runner",
				Temp: true, // Anchor workers do not stay up permanently
			},
		}}
	}
	return layout, nil
}

func (e Ecs) componentTask(component manager.DeployComponent, cluster, service string) (*manager.Task, bool) {
	switch component {
	case manager.DeployComponent_Ceramic:
		if strings.Contains(service, manager.ServiceSuffix_CeramicNode) {
			return &manager.Task{}, true
		}
	case manager.DeployComponent_Ipfs:
		if strings.Contains(service, manager.ServiceSuffix_IpfsNode) {
			return &manager.Task{}, true
		}
	case manager.DeployComponent_Cas:
		// All pre-CASv5 services are only present in the CAS cluster
		if cluster == manager.CeramicEnvPfx()+"-cas" {
			// Until all environments are moved to CASv2, the CAS Scheduler (CASv2) and CAS Worker (CASv1) ECS Services will
			// exist in some environments and not others. This is ok because only if a service exists in an environment will
			// we attempt to update it during a deployment.
			if strings.Contains(service, manager.ServiceSuffix_CasApi) ||
				// CASv2
				strings.Contains(service, manager.ServiceSuffix_CasScheduler) {
				return &manager.Task{}, true
			} else if strings.Contains(service, manager.ServiceSuffix_CasWorker) { // CASv1
				return &manager.Task{
					Repo: manager.CeramicEnvPfx() + "-cas-runner",
					Temp: true, // Anchor workers do not stay up permanently
				}, true
			}
		}
	case manager.DeployComponent_CasV5:
		// All CASv5 services will exist in a separate "app-cas" cluster
		if cluster == "app-cas-"+string(e.env) {
			if strings.Contains(service, manager.ServiceSuffix_CasScheduler) {
				return &manager.Task{}, true
			}
		}
	default:
		log.Printf("componentTask: unknown component: %s", component)
	}
	return nil, false
}

func (e Ecs) componentEcrRepo(component manager.DeployComponent) (string, error) {
	envStr := string(e.env)
	switch component {
	case manager.DeployComponent_Ceramic:
		return manager.CeramicEnvPfx(), nil
	case manager.DeployComponent_Ipfs:
		return "go-ipfs-" + envStr, nil
	case manager.DeployComponent_Cas:
		return manager.CeramicEnvPfx() + "-cas", nil
	case manager.DeployComponent_CasV5:
		return "app-cas-scheduler", nil
	default:
		return "", fmt.Errorf("componentTask: unknown component: %s", component)
	}
}

func (e Ecs) UpdateEnv(layout *manager.Layout, commitHash string) error {
	for clusterName, cluster := range layout.Clusters {
		clusterRepo := layout.Repo
		if len(cluster.Repo) > 0 {
			clusterRepo = cluster.Repo
		}
		if err := e.updateEnvCluster(cluster, clusterName, clusterRepo, commitHash); err != nil {
			return err
		}
	}
	return nil
}

func (e Ecs) CheckEnv(layout *manager.Layout) (bool, error) {
	for clusterName, cluster := range layout.Clusters {
		if deployed, err := e.checkEnvCluster(cluster, clusterName); err != nil {
			return false, err
		} else if !deployed {
			return false, nil
		}
	}
	return true, nil
}

func (e Ecs) describeEcsService(cluster, service string) (*ecs.DescribeServicesOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	input := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	if output, err := e.ecsClient.DescribeServices(ctx, input); err != nil {
		log.Printf("describeEcsService: %s, %s, %v", service, cluster, err)
		return nil, err
	} else if len(output.Failures) > 0 {
		ecsFailures := e.parseEcsFailures(output.Failures)
		log.Printf("describeEcsService: %s, %s, %v", service, cluster, ecsFailures)
		return nil, fmt.Errorf("%v", ecsFailures)
	} else {
		return output, nil
	}
}

func (e Ecs) listEcsServices(cluster string) (*ecs.ListServicesOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	input := &ecs.ListServicesInput{
		Cluster: aws.String(cluster),
	}
	if output, err := e.ecsClient.ListServices(ctx, input); err != nil {
		log.Printf("listEcsServices: %s, %v", cluster, err)
		return nil, err
	} else {
		return output, nil
	}
}

func (e Ecs) runEcsTask(cluster, family, container string, networkConfig *types.NetworkConfiguration, overrides map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	input := &ecs.RunTaskInput{
		TaskDefinition:       aws.String(family),
		Cluster:              aws.String(cluster),
		Count:                aws.Int32(1),
		EnableExecuteCommand: true,
		LaunchType:           "FARGATE",
		NetworkConfiguration: networkConfig,
		StartedBy:            aws.String(manager.ServiceName),
		Tags:                 []types.Tag{{Key: aws.String(manager.ResourceTag), Value: aws.String(string(e.env))}},
	}
	if (overrides != nil) && (len(overrides) > 0) {
		overrideEnv := make([]types.KeyValuePair, 0, len(overrides))
		for k, v := range overrides {
			overrideEnv = append(overrideEnv, types.KeyValuePair{Name: aws.String(k), Value: aws.String(v)})
		}
		input.Overrides = &types.TaskOverride{
			ContainerOverrides: []types.ContainerOverride{
				{
					Name:        aws.String(container),
					Environment: overrideEnv,
				},
			},
		}
	}
	if output, err := e.ecsClient.RunTask(ctx, input); err != nil {
		log.Printf("runEcsTask: %s, %s, %s, %+v, %v", cluster, family, container, overrides, err)
		return "", err
	} else {
		return *output.Tasks[0].TaskArn, nil
	}
}

func (e Ecs) updateEcsTaskDefinition(taskDefArn, image string) (string, error) {
	taskDef, err := e.getEcsTaskDefinition(taskDefArn)
	if err != nil {
		log.Printf("updateEcsTaskDefinition: get task def error: %s, %s, %v", taskDefArn, image, err)
		return "", err
	}
	// Register a new task definition with an updated image
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	taskDef.ContainerDefinitions[0].Image = aws.String(e.ecrUri + image)
	regTaskDefInput := &ecs.RegisterTaskDefinitionInput{
		ContainerDefinitions:    taskDef.ContainerDefinitions,
		Family:                  taskDef.Family,
		Cpu:                     taskDef.Cpu,
		EphemeralStorage:        taskDef.EphemeralStorage,
		ExecutionRoleArn:        taskDef.ExecutionRoleArn,
		InferenceAccelerators:   taskDef.InferenceAccelerators,
		IpcMode:                 taskDef.IpcMode,
		Memory:                  taskDef.Memory,
		NetworkMode:             taskDef.NetworkMode,
		PidMode:                 taskDef.PidMode,
		PlacementConstraints:    taskDef.PlacementConstraints,
		ProxyConfiguration:      taskDef.ProxyConfiguration,
		RequiresCompatibilities: taskDef.RequiresCompatibilities,
		RuntimePlatform:         taskDef.RuntimePlatform,
		TaskRoleArn:             taskDef.TaskRoleArn,
		Volumes:                 taskDef.Volumes,
		Tags:                    []types.Tag{{Key: aws.String(manager.ResourceTag), Value: aws.String(string(e.env))}},
	}
	if regTaskDefOutput, err := e.ecsClient.RegisterTaskDefinition(ctx, regTaskDefInput); err != nil {
		log.Printf("updateEcsTaskDefinition: register task def error: %s, %s, %v", taskDefArn, image, err)
		return "", err
	} else {
		return *regTaskDefOutput.TaskDefinition.TaskDefinitionArn, nil
	}
}

func (e Ecs) getEcsTaskDefinition(taskDefArn string) (*types.TaskDefinition, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	input := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}
	if output, err := e.ecsClient.DescribeTaskDefinition(ctx, input); err != nil {
		log.Printf("getEcsTaskDefinition: describe task def error: %s, %v", taskDefArn, err)
		return nil, err
	} else {
		return output.TaskDefinition, nil
	}
}

func (e Ecs) updateEcsService(cluster, service, image string, tempTask bool) (string, error) {
	// Describe service to get task definition ARN
	descSvcOutput, err := e.describeEcsService(cluster, service)
	if err != nil {
		log.Printf("updateEcsService: describe service error: %s, %s, %s, %v, %v", cluster, service, image, tempTask, err)
		return "", err
	}
	// Update task definition with new image
	newTaskDefArn, err := e.updateEcsTaskDefinition(*descSvcOutput.Services[0].TaskDefinition, image)
	if err != nil {
		log.Printf("updateEcsService: update task def error: %s, %s, %s, %v, %v", cluster, service, image, tempTask, err)
		return "", err
	}
	// Update the service to use the new task definition
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	updateSvcInput := &ecs.UpdateServiceInput{
		Service:              aws.String(service),
		Cluster:              aws.String(cluster),
		EnableExecuteCommand: aws.Bool(true),
		ForceNewDeployment:   true, // enable this so that the deployment circuit breaker can kick-in
		TaskDefinition:       aws.String(newTaskDefArn),
	}
	if _, err = e.ecsClient.UpdateService(ctx, updateSvcInput); err != nil {
		log.Printf("updateEcsService: update service error: %s, %s, %s, %s, %v, %v", cluster, service, image, newTaskDefArn, tempTask, err)
		return "", err
	} else if !tempTask {
		// Stop all permanently running tasks in the service
		if err = e.stopEcsTasks(cluster, e.taskFamilyFromArn(newTaskDefArn)); err != nil {
			log.Printf("updateEcsService: stop tasks error: %s, %s, %s, %s, %v, %v", cluster, service, image, newTaskDefArn, tempTask, err)
			return "", err
		}
	}
	return newTaskDefArn, nil
}

func (e Ecs) updateEcsTask(cluster, familyPfx, image string, tempTask bool) (string, error) {
	if prevTaskDefArn, err := e.getEcsTaskDefinitionArn(familyPfx); err != nil {
		log.Printf("updateEcsTask: get task def error: %s, %s, %s, %v, %v", cluster, familyPfx, image, tempTask, err)
		return "", err
	} else if newTaskDefArn, err := e.updateEcsTaskDefinition(prevTaskDefArn, image); err != nil {
		log.Printf("updateEcsTask: update task def error: %s, %s, %s, %s, %v, %v", cluster, familyPfx, image, prevTaskDefArn, tempTask, err)
		return "", err
	} else {
		if !tempTask {
			// Stop all permanently running tasks in the service
			if err = e.stopEcsTasks(cluster, e.taskFamilyFromArn(newTaskDefArn)); err != nil {
				log.Printf("updateEcsTask: stop tasks error: %s, %s, %s, %s, %s, %v, %v", cluster, familyPfx, image, prevTaskDefArn, newTaskDefArn, tempTask, err)
				return "", err
			}
		}
		return newTaskDefArn, nil
	}
}

func (e Ecs) getEcsTaskDefinitionArn(familyPfx string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// List all task definitions and get the latest definition's ARN
	input := &ecs.ListTaskDefinitionsInput{
		FamilyPrefix: aws.String(familyPfx),
		MaxResults:   aws.Int32(1),
		Sort:         types.SortOrderDesc,
	}
	output, err := e.ecsClient.ListTaskDefinitions(ctx, input)
	if err != nil {
		log.Printf("getEcsTaskDefinitionArn: list task defs error: %s, %v", familyPfx, err)
		return "", err
	}
	return output.TaskDefinitionArns[0], nil
}

func (e Ecs) stopEcsTasks(cluster, family string) error {
	if taskArns, err := e.listEcsTasks(cluster, family); err != nil {
		log.Printf("stopEcsTasks: list tasks error: %s, %s, %v", cluster, family, err)
		return err
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
		defer cancel()

		for _, taskArn := range taskArns {
			stopTasksInput := &ecs.StopTaskInput{
				Task:    aws.String(taskArn),
				Cluster: aws.String(cluster),
			}
			if _, err = e.ecsClient.StopTask(ctx, stopTasksInput); err != nil {
				log.Printf("stopEcsTasks: stop task error: %s, %s, %v", cluster, family, err)
				return err
			}
		}
	}
	return nil
}

func (e Ecs) checkEcsService(cluster, taskDefArn string) (bool, error) {
	family := e.taskFamilyFromArn(taskDefArn)
	if taskArns, err := e.listEcsTasks(cluster, family); err != nil {
		log.Printf("checkEcsService: list tasks error: %s, %s, %s, %v", cluster, family, taskDefArn, err)
		return false, err
	} else if len(taskArns) > 0 {
		// For each running task, check if it's been up for a few minutes.
		if deployed, err := e.CheckTask(cluster, taskDefArn, true, true, taskArns...); err != nil {
			log.Printf("checkEcsService: check task error: %s, %s, %s, %v", cluster, family, taskDefArn, err)
			return false, err
		} else if !deployed {
			return false, nil
		}
		return true, nil
	}
	return false, nil
}

func (e Ecs) listEcsTasks(cluster, family string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	listTasksInput := &ecs.ListTasksInput{
		Cluster:       aws.String(cluster),
		DesiredStatus: types.DesiredStatusRunning,
		Family:        aws.String(family),
	}
	listTasksOutput, err := e.ecsClient.ListTasks(ctx, listTasksInput)
	if err != nil {
		log.Printf("listEcsTasks: list tasks error: %s, %s, %v", cluster, family, err)
		return nil, err
	}
	return listTasksOutput.TaskArns, nil
}

func (e Ecs) updateEnvCluster(cluster *manager.Cluster, clusterName, clusterRepo, commitHash string) error {
	if err := e.updateEnvTaskSet(cluster.ServiceTasks, manager.DeployType_Service, clusterName, clusterRepo, commitHash); err != nil {
		return err
	} else if err = e.updateEnvTaskSet(cluster.Tasks, manager.DeployType_Task, clusterName, clusterRepo, commitHash); err != nil {
		return err
	}
	return nil
}

func (e Ecs) updateEnvTaskSet(taskSet *manager.TaskSet, deployType manager.DeployType, cluster, clusterRepo, commitHash string) error {
	if taskSet != nil {
		for taskSetName, task := range taskSet.Tasks {
			taskSetRepo := clusterRepo
			if len(taskSet.Repo) > 0 {
				taskSetRepo = taskSet.Repo
			}
			switch deployType {
			case manager.DeployType_Service:
				if err := e.updateEnvServiceTask(task, cluster, taskSetName, taskSetRepo, commitHash); err != nil {
					return err
				}
			case manager.DeployType_Task:
				if err := e.updateEnvTask(task, cluster, taskSetName, taskSetRepo, commitHash); err != nil {
					return err
				}
			default:
				return fmt.Errorf("updateTaskSet: invalid deploy type: %s", deployType)
			}
		}
	}
	return nil
}

func (e Ecs) updateEnvServiceTask(task *manager.Task, cluster, service, taskSetRepo, commitHash string) error {
	taskRepo := taskSetRepo
	if len(task.Repo) > 0 {
		taskRepo = task.Repo
	}
	if id, err := e.updateEcsService(cluster, service, taskRepo+":"+commitHash, task.Temp); err != nil {
		return err
	} else {
		task.Id = id
		return nil
	}
}

func (e Ecs) updateEnvTask(task *manager.Task, cluster, taskName, taskSetRepo, commitHash string) error {
	taskRepo := taskSetRepo
	if len(task.Repo) > 0 {
		taskRepo = task.Repo
	}
	if id, err := e.updateEcsTask(cluster, taskName, taskRepo+":"+commitHash, task.Temp); err != nil {
		return err
	} else {
		task.Id = id
		return nil
	}
}

func (e Ecs) checkEnvCluster(cluster *manager.Cluster, clusterName string) (bool, error) {
	if deployed, err := e.checkEnvTaskSet(cluster.ServiceTasks, manager.DeployType_Service, clusterName); err != nil {
		return false, err
	} else if !deployed {
		return false, nil
	} else if deployed, err = e.checkEnvTaskSet(cluster.Tasks, manager.DeployType_Task, clusterName); err != nil {
		return false, err
	} else {
		return deployed, nil
	}
}

func (e Ecs) checkEnvTaskSet(taskSet *manager.TaskSet, deployType manager.DeployType, cluster string) (bool, error) {
	if taskSet != nil {
		for _, task := range taskSet.Tasks {
			switch deployType {
			case manager.DeployType_Service:
				if deployed, err := e.checkEcsService(cluster, task.Id); err != nil {
					return false, err
				} else if !deployed {
					return false, nil
				}
				return true, nil
			case manager.DeployType_Task:
				// Only check tasks that are meant to stay up permanently
				if !task.Temp {
					if deployed, err := e.CheckTask(cluster, "", true, true, task.Id); err != nil {
						return false, err
					} else if !deployed {
						return false, nil
					}
					return true, nil
				}
			default:
				return false, fmt.Errorf("updateTaskSet: invalid deploy type: %s", deployType)
			}
		}
	}
	return true, nil
}

func (e Ecs) taskFamilyFromArn(taskArn string) string {
	// Given our configuration, the task family is the same as the name of the task definition. For a task definition
	// ARN like "arn:aws:ecs:us-east-2:967314784947:task-definition/ceramic-qa-ex-ipfs-nd-go-new-peer:18", we can get
	// the name by splitting around the "/", taking the second part, then splitting around the ":" and taking the first.
	return strings.Split(strings.Split(taskArn, "/")[1], ":")[0]
}

func (e Ecs) serviceNameFromArn(serviceArn string) string {
	// For a service ARN like "arn:aws:ecs:us-east-2:967314784947:service/ceramic-dev/ceramic-dev-node", we can get the
	// the name by splitting around the "/", then taking the last part.
	return strings.Split(serviceArn, "/")[2]
}

func (e Ecs) parseEcsFailures(ecsFailures []types.Failure) []ecsFailure {
	failures := make([]ecsFailure, len(ecsFailures))
	for idx, f := range ecsFailures {
		if f.Arn != nil {
			failures[idx].arn = *f.Arn
		}
		if f.Detail != nil {
			failures[idx].detail = *f.Detail
		}
		if f.Reason != nil {
			failures[idx].reason = *f.Reason
		}
	}
	return failures
}
