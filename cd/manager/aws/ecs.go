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

const EcsTaskStabilityTime = 5 * time.Minute

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
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if output, err := e.describeEcsService(ctx, cluster, service); err != nil {
		return "", err
	} else {
		return e.runEcsTask(ctx, cluster, family, container, output.Services[0].NetworkConfiguration, overrides)
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
	return e.runEcsTask(ctx, cluster, family, container, &types.NetworkConfiguration{AwsvpcConfiguration: &vpcConfig}, overrides)
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
					(running && stable && time.Now().Add(-EcsTaskStabilityTime).Before(*task.StartedAt)) {
					tasksInState = false
				}
			}
		}
	}
	return tasksFound && tasksInState, nil
}

func (e Ecs) PopulateEnvLayout(component manager.DeployComponent) (*manager.Layout, error) {
	const (
		ServiceSuffix_CeramicNode      string = "node"
		ServiceSuffix_CeramicGateway   string = "gateway"
		ServiceSuffix_Elp11CeramicNode string = "elp-1-1-node"
		ServiceSuffix_Elp12CeramicNode string = "elp-1-2-node"
		ServiceSuffix_IpfsNode         string = "ipfs-nd"
		ServiceSuffix_IpfsGateway      string = "ipfs-gw"
		ServiceSuffix_Elp11IpfsNode    string = "elp-1-1-ipfs-nd"
		ServiceSuffix_Elp12IpfsNode    string = "elp-1-2-ipfs-nd"
		ServiceSuffix_CasApi           string = "api"
		ServiceSuffix_CasScheduler     string = "scheduler"
		ServiceSuffix_CasRunner        string = "anchor"
	)

	env := os.Getenv("ENV")
	globalPrefix := "ceramic"
	privateCluster := globalPrefix + "-" + env
	publicCluster := globalPrefix + "-" + env + "-ex"
	casCluster := globalPrefix + "-" + env + "-cas"

	switch component {
	case manager.DeployComponent_Ceramic:
		layout := &manager.Layout{
			Clusters: map[string]*manager.Cluster{
				privateCluster: {
					ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{
						privateCluster + "-" + ServiceSuffix_CeramicNode: {},
					}},
				},
				publicCluster: {
					ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{
						publicCluster + "-" + ServiceSuffix_CeramicNode:    {},
						publicCluster + "-" + ServiceSuffix_CeramicGateway: {},
					}},
				},
				casCluster: {
					ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{
						casCluster + "-" + ServiceSuffix_CeramicNode: {},
					}},
				},
			},
			Repo: "ceramic-" + env,
		}
		if e.env == manager.EnvType_Prod {
			layout.Clusters[publicCluster].ServiceTasks.Tasks[globalPrefix+"-"+ServiceSuffix_Elp11CeramicNode] = &manager.Task{}
			layout.Clusters[publicCluster].ServiceTasks.Tasks[globalPrefix+"-"+ServiceSuffix_Elp12CeramicNode] = &manager.Task{}
		}
		return layout, nil
	case manager.DeployComponent_Ipfs:
		layout := &manager.Layout{
			Clusters: map[string]*manager.Cluster{
				privateCluster: {
					ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{
						privateCluster + "-" + ServiceSuffix_IpfsNode: {},
					}},
				},
				publicCluster: {
					ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{
						publicCluster + "-" + ServiceSuffix_IpfsNode:    {},
						publicCluster + "-" + ServiceSuffix_IpfsGateway: {},
					}},
				},
				casCluster: {
					ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{
						casCluster + "-" + ServiceSuffix_IpfsNode: {},
					}},
				},
			},
			Repo: "go-ipfs-" + env,
		}
		if e.env == manager.EnvType_Prod {
			layout.Clusters[publicCluster].ServiceTasks.Tasks[globalPrefix+"-"+ServiceSuffix_Elp11IpfsNode] = &manager.Task{}
			layout.Clusters[publicCluster].ServiceTasks.Tasks[globalPrefix+"-"+ServiceSuffix_Elp12IpfsNode] = &manager.Task{}
		}
		return layout, nil
	case manager.DeployComponent_Cas:
		layout := &manager.Layout{
			Clusters: map[string]*manager.Cluster{
				casCluster: {
					ServiceTasks: &manager.TaskSet{Tasks: map[string]*manager.Task{
						casCluster + "-" + ServiceSuffix_CasApi: {},
					}},
				},
			},
			Repo: "ceramic-" + env + "-cas",
		}
		// TODO: Move Prod to CASv2 once it is ready
		if e.env == manager.EnvType_Prod {
			// Production CAS has an ECS Service for running Anchor workers, so set it up like the API service. Mark
			// the worker "temporary" because it is not expected to come up and stay up after the service is updated.
			layout.Clusters[casCluster].ServiceTasks.Tasks[casCluster+"-"+ServiceSuffix_CasRunner] = &manager.Task{
				Temp: true, // Anchor workers do not stay up permanently
			}
		} else {
			// All other CAS clusters have a Scheduler ECS Service, and standalone Anchor worker ECS Tasks.
			layout.Clusters[casCluster].ServiceTasks.Tasks[casCluster+"-"+ServiceSuffix_CasScheduler] = &manager.Task{}
			layout.Clusters[casCluster].Tasks = &manager.TaskSet{Tasks: map[string]*manager.Task{
				casCluster + "-" + ServiceSuffix_CasRunner: {
					Repo: "ceramic-" + env + "-cas-runner",
					Temp: true, // Anchor workers do not stay up permanently
				},
			}}
		}
		return layout, nil
	default:
		return nil, fmt.Errorf("deployJob: unexpected component: %s", component)
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

func (e Ecs) describeEcsService(ctx context.Context, cluster, service string) (*ecs.DescribeServicesOutput, error) {
	input := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	if output, err := e.ecsClient.DescribeServices(ctx, input); err != nil {
		log.Printf("describeEcsService: %s, %s, %v", service, cluster, err)
		return nil, err
	} else if len(output.Failures) > 0 {
		ecsFailures := e.parseEcsFailures(output.Failures)
		log.Printf("describeEcsService: failure: %s, %s, %v", service, cluster, ecsFailures)
		return nil, fmt.Errorf("%v", ecsFailures)
	} else {
		return output, nil
	}
}

func (e Ecs) runEcsTask(ctx context.Context, cluster, family, container string, networkConfig *types.NetworkConfiguration, overrides map[string]string) (string, error) {
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

func (e Ecs) updateEcsTaskDefinition(ctx context.Context, taskDefArn, image string) (string, error) {
	descTaskDefInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}
	descTaskDefOutput, err := e.ecsClient.DescribeTaskDefinition(ctx, descTaskDefInput)
	if err != nil {
		log.Printf("updateEcsTaskDefinition: describe task def error: %s, %s, %v", taskDefArn, image, err)
		return "", err
	}
	// Register a new task definition with an updated image
	taskDef := descTaskDefOutput.TaskDefinition
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
	regTaskDefOutput, err := e.ecsClient.RegisterTaskDefinition(ctx, regTaskDefInput)
	if err != nil {
		log.Printf("updateEcsTaskDefinition: register task def error: %s, %s, %v", taskDefArn, image, err)
		return "", err
	}
	return *regTaskDefOutput.TaskDefinition.TaskDefinitionArn, nil
}

func (e Ecs) updateEcsService(cluster, service, image string, tempTask bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// Describe service to get task definition ARN
	descSvcOutput, err := e.describeEcsService(ctx, cluster, service)

	// Describe task to get full task definition
	newTaskDefArn, err := e.updateEcsTaskDefinition(ctx, *descSvcOutput.Services[0].TaskDefinition, image)
	family := e.taskFamilyFromArn(newTaskDefArn)

	// Update the service to use the new task definition
	updateSvcInput := &ecs.UpdateServiceInput{
		Service:              aws.String(service),
		Cluster:              aws.String(cluster),
		DesiredCount:         aws.Int32(1),
		EnableExecuteCommand: aws.Bool(true),
		ForceNewDeployment:   false,
		TaskDefinition:       aws.String(newTaskDefArn),
	}
	if _, err = e.ecsClient.UpdateService(ctx, updateSvcInput); err != nil {
		log.Printf("updateEcsService: update service error: %s, %s, %s, %s, %v", cluster, service, family, image, err)
		return "", err
	} else if !tempTask {
		// Stop all permanently running tasks in the service
		if err = e.stopEcsTasks(ctx, cluster, family); err != nil {
			log.Printf("updateEcsService: stop tasks error: %s, %s, %s, %s, %v", cluster, service, family, image, err)
			return "", err
		}
	}
	return newTaskDefArn, nil
}

func (e Ecs) updateEcsTask(cluster, familyPfx, image string, transientTask bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// Get the latest task definition ARN
	input := &ecs.ListTaskDefinitionsInput{
		FamilyPrefix: aws.String(familyPfx),
		MaxResults:   aws.Int32(1),
		Sort:         types.SortOrderDesc,
	}
	output, err := e.ecsClient.ListTaskDefinitions(ctx, input)
	taskDefArn := output.TaskDefinitionArns[0]
	family := e.taskFamilyFromArn(taskDefArn)
	if err != nil {
		return "", err
	} else if !transientTask {
		// Stop all permanently running tasks in the service
		if err = e.stopEcsTasks(ctx, cluster, family); err != nil {
			log.Printf("updateEcsTask: stop tasks error: %s, %s, %s, %s, %v", cluster, family, taskDefArn, image, err)
			return "", err
		}
	}
	return e.updateEcsTaskDefinition(ctx, taskDefArn, image)
}

func (e Ecs) stopEcsTasks(ctx context.Context, cluster, family string) error {
	if taskArns, err := e.listEcsTasks(ctx, cluster, family); err != nil {
		log.Printf("stopEcsTasks: list tasks error: %s, %s, %v", cluster, family, err)
		return err
	} else {
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
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	family := e.taskFamilyFromArn(taskDefArn)
	if taskArns, err := e.listEcsTasks(ctx, cluster, family); err != nil {
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

func (e Ecs) listEcsTasks(ctx context.Context, cluster, family string) ([]string, error) {
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
