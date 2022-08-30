package aws

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/3box/pipeline-tools/cd/manager"
)

const EcsWaitTime = 30 * time.Second

var _ manager.Deployment = &Ecs{}

type Ecs struct {
	client *ecs.Client
	env    manager.EnvType
}

func NewEcs(cfg aws.Config) manager.Deployment {
	return &Ecs{ecs.NewFromConfig(cfg), manager.EnvType(os.Getenv("ENV"))}
}

func (e Ecs) LaunchService(cluster, service, family, container string, overrides map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	descInput := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	descOutput, err := e.client.DescribeServices(ctx, descInput)
	if err != nil {
		return "", fmt.Errorf("launchService: describe service error: %s, %s, %w", family, cluster, err)
	}
	input := &ecs.RunTaskInput{
		TaskDefinition:       aws.String(family),
		Cluster:              aws.String(cluster),
		Count:                aws.Int32(1),
		EnableExecuteCommand: true,
		LaunchType:           "FARGATE",
		NetworkConfiguration: descOutput.Services[0].NetworkConfiguration,
		StartedBy:            aws.String("cd-manager"),
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
	output, err := e.client.RunTask(ctx, input)
	if err != nil {
		return "", fmt.Errorf("ecs: run task error: %s, %s, %+v, %w", family, cluster, overrides, err)
	}
	return *output.Tasks[0].TaskArn, nil
}

func (e Ecs) CheckTask(running bool, cluster string, taskArn ...string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Describe cluster tasks matching the specified ARNs.
	descInput := &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   taskArn,
	}
	descOutput, err := e.client.DescribeTasks(ctx, descInput)
	if err != nil {
		return false, fmt.Errorf("checkTask: describe service error: %s, %s, %w", cluster, taskArn, err)
	}
	var checkStatus types.DesiredStatus
	if running {
		checkStatus = types.DesiredStatusRunning
	} else {
		checkStatus = types.DesiredStatusStopped
	}
	// Check whether the specified tasks are running.
	if len(descOutput.Tasks) > 0 {
		// We found one or more tasks, only return true if all specified tasks were in the right state.
		for _, task := range descOutput.Tasks {
			if *task.LastStatus != string(checkStatus) {
				return false, nil
			}
		}
		return true, nil
	}
	return false, nil
}

func (e Ecs) UpdateService(cluster, service, image string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Describe service to get task definition ARN.
	descSvcInput := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	descOutput, err := e.client.DescribeServices(ctx, descSvcInput)
	if err != nil {
		return "", fmt.Errorf("updateService: describe service error: %s, %s, %s, %w", cluster, service, image, err)
	}

	// Describe task to get full task definition.
	taskDefArn := descOutput.Services[0].TaskDefinition
	descTaskInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: taskDefArn,
	}
	descTaskOutput, err := e.client.DescribeTaskDefinition(ctx, descTaskInput)
	if err != nil {
		return "", fmt.Errorf("updateService: describe task definition error: %s, %s, %s, %w", cluster, service, image, err)
	}

	// Register a new task definition with an updated image.
	taskDef := descTaskOutput.TaskDefinition
	taskDef.ContainerDefinitions[0].Image = aws.String(image)
	regTaskInput := &ecs.RegisterTaskDefinitionInput{
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
	}
	regTaskOutput, err := e.client.RegisterTaskDefinition(ctx, regTaskInput)
	if err != nil {
		return "", fmt.Errorf("updateService: register task definition error: %s, %s, %s, %w", cluster, service, image, err)
	}

	// Update the service to use the new task definition.
	newTaskDef := regTaskOutput.TaskDefinition
	updateSvcInput := &ecs.UpdateServiceInput{
		Service:              aws.String(service),
		Cluster:              aws.String(cluster),
		DesiredCount:         aws.Int32(1),
		EnableExecuteCommand: aws.Bool(true),
		ForceNewDeployment:   false,
		TaskDefinition:       newTaskDef.TaskDefinitionArn,
	}
	_, err = e.client.UpdateService(ctx, updateSvcInput)
	if err != nil {
		return "", fmt.Errorf("updateService: update service error: %s, %s, %s, %w", cluster, service, image, err)
	}
	return *newTaskDef.TaskDefinitionArn, nil
}

func (e Ecs) CheckService(cluster, service, taskDefArn string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Describe service to get deployment status
	descSvcInput := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	descOutput, err := e.client.DescribeServices(ctx, descSvcInput)
	if err != nil {
		return false, fmt.Errorf("checkService: describe service error: %s, %s, %s, %w", cluster, service, taskDefArn, err)
	}

	// Look for deployments using the new task definition with at least 1 running task.
	for _, deployment := range descOutput.Services[0].Deployments {
		if (*deployment.TaskDefinition == taskDefArn) && (deployment.RunningCount > 0) {
			return true, nil
		}
	}
	return false, nil
}

func (e Ecs) PopulateLayout(component string) (map[string]interface{}, error) {
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
		ServiceSuffix_CasAnchor        string = "anchor"
	)

	env := os.Getenv("ENV")
	globalPrefix := "ceramic"
	privateCluster := globalPrefix + "-" + env
	publicCluster := globalPrefix + "-" + env + "-ex"
	casCluster := globalPrefix + "-" + env + "-cas"

	var privateLayout map[string]interface{}
	var publicLayout map[string]interface{}
	var casLayout map[string]interface{}
	switch component {
	case manager.DeployComponent_Ceramic:
		privateLayout = map[string]interface{}{
			privateCluster + "-" + ServiceSuffix_CeramicNode: nil,
		}
		publicLayout = map[string]interface{}{
			publicCluster + "-" + ServiceSuffix_CeramicNode:    nil,
			publicCluster + "-" + ServiceSuffix_CeramicGateway: nil,
		}
		if e.env == manager.EnvType_Prod {
			publicLayout[globalPrefix+"-"+ServiceSuffix_Elp11CeramicNode] = nil
			publicLayout[globalPrefix+"-"+ServiceSuffix_Elp12CeramicNode] = nil
		}
		casLayout = map[string]interface{}{
			casCluster + "-" + ServiceSuffix_CeramicNode: nil,
		}
	case manager.DeployComponent_Ipfs:
		privateLayout = map[string]interface{}{
			privateCluster + ServiceSuffix_IpfsNode: nil,
		}
		publicLayout = map[string]interface{}{
			publicCluster + "-" + ServiceSuffix_IpfsNode:    nil,
			publicCluster + "-" + ServiceSuffix_IpfsGateway: nil,
		}
		if e.env == manager.EnvType_Prod {
			publicLayout[globalPrefix+"-"+ServiceSuffix_Elp11IpfsNode] = nil
			publicLayout[globalPrefix+"-"+ServiceSuffix_Elp12IpfsNode] = nil
		}
		casLayout = map[string]interface{}{
			casCluster + "-" + ServiceSuffix_IpfsNode: nil,
		}
	case manager.DeployComponent_Cas:
		casLayout = map[string]interface{}{
			casCluster + "-" + ServiceSuffix_CasApi:    nil,
			casCluster + "-" + ServiceSuffix_CasAnchor: nil,
		}
	default:
		return nil, fmt.Errorf("deployJob: unexpected component: %s", component)
	}
	return map[string]interface{}{
		privateCluster: privateLayout,
		publicCluster:  publicLayout,
		casCluster:     casLayout,
	}, nil
}

func (e Ecs) GetRegistryUri(component string) (string, error) {
	env := os.Getenv("ENV")
	var repo string
	switch component {
	case manager.DeployComponent_Ceramic:
		repo = "ceramic-" + env
	case manager.DeployComponent_Ipfs:
		repo = "go-ipfs-" + env
	case manager.DeployComponent_Cas:
		repo = "ceramic-" + env + "-cas"
	default:
		return "", fmt.Errorf("getImagePath: invalid component: %s", component)
	}
	return os.Getenv("AWS_ACCOUNT_ID") + ".dkr.ecr." + os.Getenv("AWS_REGION") + ".amazonaws.com/" + repo, nil
}
