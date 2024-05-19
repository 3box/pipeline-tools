package ecs

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

const (
	deployType_Service string = "service"
	deployType_Task    string = "task"
)

const resourceTag = "Ceramic"
const publicEcrUri = "public.ecr.aws/r5b3e0r5/3box/"

func NewEcs(cfg aws.Config) manager.Deployment {
	ecrUri := os.Getenv("AWS_ACCOUNT_ID") + ".dkr.ecr." + os.Getenv("AWS_REGION") + ".amazonaws.com/"
	return &Ecs{ecs.NewFromConfig(cfg), ssm.NewFromConfig(cfg), manager.EnvType(os.Getenv(manager.EnvVar_Env)), ecrUri}
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

func (e Ecs) CheckTask(cluster, taskDefId string, running, stable bool, taskIds ...string) (bool, *int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// Describe cluster tasks matching the specified ARNs
	input := &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   taskIds,
	}
	output, err := e.ecsClient.DescribeTasks(ctx, input)
	if err != nil {
		log.Printf("checkTask: describe service error: %s, %s, %v", cluster, taskIds, err)
		return false, nil, err
	}
	// If checking for running tasks, at least one task must be present, but when checking for stopped tasks, it's ok to
	// have found no matching tasks (i.e. the tasks have already stopped and been removed from the list).
	tasksFound := !running
	tasksInState := true
	var exitCode *int32 = nil
	if len(output.Tasks) > 0 {
		for _, task := range output.Tasks {
			// If a task definition ARN was specified, make sure that we found at least one task with that definition.
			if (len(taskDefId) == 0) || (*task.TaskDefinitionArn == taskDefId) {
				tasksFound = true
				// If checking for stable tasks, make sure that the task has been running for a few minutes.
				if running {
					if (*task.LastStatus != string(types.DesiredStatusRunning)) ||
						(stable && time.Now().After((*task.StartedAt).Add(manager.DefaultWaitTime))) {
						tasksInState = false
					}
				} else {
					if *task.LastStatus != string(types.DesiredStatusStopped) {
						tasksInState = false
					} else
					// We always configure the primary application in a task as the first container, so we only care
					// about its exit code. Among the first containers across all matching tasks, return the highest
					// exit code.
					if task.Containers[0].ExitCode != nil {
						if (exitCode == nil) || (*task.Containers[0].ExitCode > *exitCode) {
							exitCode = task.Containers[0].ExitCode
						}
					}
				}
			}
		}
	}
	return tasksFound && tasksInState, exitCode, nil
}

func (e Ecs) GetLayout(clusters []string) (*manager.Layout, error) {
	// First validate and filter the list of clusters since not all clusters might be present in all envs.
	if descClusterOutput, err := e.describeEcsClusters(clusters); err != nil {
		log.Printf("getLayout: describe clusters error: %v, %v", clusters, err)
		return nil, err
	} else {
		layout := &manager.Layout{Clusters: map[string]*manager.Cluster{}}
		for _, cluster := range descClusterOutput.Clusters {
			clusterName := *cluster.ClusterName
			if clusterServices, err := e.listEcsServices(clusterName); err != nil {
				log.Printf("getLayout: list services error: %s, %v", cluster, err)
				return nil, err
			} else if len(clusterServices.ServiceArns) > 0 {
				layout.Clusters[clusterName] = &manager.Cluster{ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{}}}
				for _, serviceArn := range clusterServices.ServiceArns {
					service := e.serviceNameFromArn(serviceArn)
					if ecsService, err := e.describeEcsService(clusterName, service); err != nil {
						log.Printf("getLayout: describe service error: %s, %s, %v", cluster, service, err)
						return nil, err
					} else {
						taskDefArn := *ecsService.Services[0].TaskDefinition
						containerDefNames := make([]string, 0, 1)
						if taskDef, err := e.getEcsTaskDefinition(taskDefArn); err != nil {
							log.Printf("getLayout: get task def error: %s, %s, %s, %v", taskDefArn, cluster, service, err)
							return nil, err
						} else {
							for _, containerDef := range taskDef.ContainerDefinitions {
								containerDefNames = append(containerDefNames, *containerDef.Name)
							}
						}
						// Return the names of all the containers associated with this task definition
						layout.Clusters[clusterName].ServiceTasks.Tasks[service] = &manager.Task{Id: taskDefArn, Name: strings.Join(containerDefNames, ",")}
					}
				}
			}
		}
		return layout, nil
	}
}

func (e Ecs) UpdateLayout(layout *manager.Layout, deployTag string) error {
	for clusterName, cluster := range layout.Clusters {
		clusterRepo := e.getEcrRepo(*layout.Repo) // The main layout repo should never be null
		if cluster.Repo != nil {
			clusterRepo = e.getEcrRepo(*cluster.Repo)
		}
		if err := e.updateEnvCluster(cluster, clusterName, clusterRepo, deployTag); err != nil {
			return err
		}
	}
	return nil
}

func (e Ecs) CheckLayout(layout *manager.Layout) (bool, error) {
	for clusterName, cluster := range layout.Clusters {
		if deployed, err := e.checkEnvCluster(cluster, clusterName); err != nil {
			return false, err
		} else if !deployed {
			return false, nil
		}
	}
	return true, nil
}

func (e Ecs) describeEcsClusters(clusters []string) (*ecs.DescribeClustersOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if output, err := e.ecsClient.DescribeClusters(ctx, &ecs.DescribeClustersInput{Clusters: clusters}); err != nil {
		log.Printf("describeEcsClusters: %v", err)
		return nil, err
	} else {
		return output, nil
	}
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
		Tags:                 []types.Tag{{Key: aws.String(resourceTag), Value: aws.String(string(e.env))}},
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

func (e Ecs) updateEcsTaskDefinition(taskDefArn, image, containerName string) (string, error) {
	taskDef, err := e.getEcsTaskDefinition(taskDefArn)
	if err != nil {
		log.Printf("updateEcsTaskDefinition: get task def error: %s, %s, %v", taskDefArn, image, err)
		return "", err
	}
	// Register a new task definition with an updated image
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	for idx, containerDef := range taskDef.ContainerDefinitions {
		if *containerDef.Name == containerName {
			taskDef.ContainerDefinitions[idx].Image = aws.String(image)
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
				Tags:                    []types.Tag{{Key: aws.String(resourceTag), Value: aws.String(string(e.env))}},
			}
			if regTaskDefOutput, err := e.ecsClient.RegisterTaskDefinition(ctx, regTaskDefInput); err != nil {
				log.Printf("updateEcsTaskDefinition: register task def error: %s, %s, %s, %v", taskDefArn, image, containerName, err)
				return "", err
			} else {
				return *regTaskDefOutput.TaskDefinition.TaskDefinitionArn, nil
			}
		}
	}
	return "", fmt.Errorf("updateEcsTaskDefinition: container not found: %s, %s, %s", taskDefArn, image, containerName)
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

func (e Ecs) updateEcsService(cluster, service, image, containerName string, tempTask bool) (string, error) {
	// Describe service to get task definition ARN
	descSvcOutput, err := e.describeEcsService(cluster, service)
	if err != nil {
		log.Printf("updateEcsService: describe service error: %s, %s, %s, %v, %v", cluster, service, image, tempTask, err)
		return "", err
	}
	// Update task definition with new image
	newTaskDefArn, err := e.updateEcsTaskDefinition(*descSvcOutput.Services[0].TaskDefinition, image, containerName)
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

func (e Ecs) updateEcsTask(cluster, familyPfx, image, containerName string, tempTask bool) (string, error) {
	if prevTaskDefArn, err := e.getEcsTaskDefinitionArn(familyPfx); err != nil {
		log.Printf("updateEcsTask: get task def error: %s, %s, %s, %v, %v", cluster, familyPfx, image, tempTask, err)
		return "", err
	} else if newTaskDefArn, err := e.updateEcsTaskDefinition(prevTaskDefArn, image, containerName); err != nil {
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
		if deployed, _, err := e.CheckTask(cluster, taskDefArn, true, true, taskArns...); err != nil {
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

func (e Ecs) updateEnvCluster(cluster *manager.Cluster, clusterName, clusterRepo, deployTag string) error {
	if err := e.updateEnvTaskSet(cluster.ServiceTasks, deployType_Service, clusterName, clusterRepo, deployTag); err != nil {
		return err
	} else if err = e.updateEnvTaskSet(cluster.Tasks, deployType_Task, clusterName, clusterRepo, deployTag); err != nil {
		return err
	}
	return nil
}

func (e Ecs) updateEnvTaskSet(taskSet *manager.TaskSet, deployType string, cluster, clusterRepo, deployTag string) error {
	if taskSet != nil {
		for taskSetName, task := range taskSet.Tasks {
			taskSetRepo := clusterRepo
			if taskSet.Repo != nil {
				taskSetRepo = e.getEcrRepo(*taskSet.Repo)
			}
			switch deployType {
			case deployType_Service:
				if err := e.updateEnvServiceTask(task, cluster, taskSetName, taskSetRepo, deployTag); err != nil {
					return err
				}
			case deployType_Task:
				if err := e.updateEnvTask(task, cluster, taskSetName, taskSetRepo, deployTag); err != nil {
					return err
				}
			default:
				return fmt.Errorf("updateTaskSet: invalid deploy type: %s", deployType)
			}
		}
	}
	return nil
}

func (e Ecs) updateEnvServiceTask(task *manager.Task, cluster, service, taskSetRepo, deployTag string) error {
	taskRepo := taskSetRepo
	if task.Repo != nil {
		taskRepo = e.getEcrRepo(*task.Repo)
	}
	if id, err := e.updateEcsService(cluster, service, taskRepo+":"+deployTag, task.Name, task.Temp); err != nil {
		return err
	} else {
		task.Id = id
		return nil
	}
}

func (e Ecs) updateEnvTask(task *manager.Task, cluster, taskName, taskSetRepo, deployTag string) error {
	taskRepo := taskSetRepo
	if task.Repo != nil {
		taskRepo = e.getEcrRepo(*task.Repo)
	}
	if id, err := e.updateEcsTask(cluster, taskName, taskRepo+":"+deployTag, task.Name, task.Temp); err != nil {
		return err
	} else {
		task.Id = id
		return nil
	}
}

func (e Ecs) checkEnvCluster(cluster *manager.Cluster, clusterName string) (bool, error) {
	if deployed, err := e.checkEnvTaskSet(cluster.ServiceTasks, deployType_Service, clusterName); err != nil {
		return false, err
	} else if !deployed {
		return false, nil
	} else if deployed, err = e.checkEnvTaskSet(cluster.Tasks, deployType_Task, clusterName); err != nil {
		return false, err
	} else {
		return deployed, nil
	}
}

func (e Ecs) checkEnvTaskSet(taskSet *manager.TaskSet, deployType string, cluster string) (bool, error) {
	if taskSet != nil {
		for _, task := range taskSet.Tasks {
			switch deployType {
			case deployType_Service:
				if deployed, err := e.checkEcsService(cluster, task.Id); err != nil {
					return false, err
				} else if !deployed {
					return false, nil
				}
				return true, nil
			case deployType_Task:
				// Only check tasks that are meant to stay up permanently
				if !task.Temp {
					if deployed, _, err := e.CheckTask(cluster, "", true, true, task.Id); err != nil {
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

func (e Ecs) getEcrRepo(repo manager.Repo) string {
	if repo.Public {
		return publicEcrUri + repo.Name
	}
	return e.ecrUri + repo.Name
}
