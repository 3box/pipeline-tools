package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Deployment = &Ecs{}

const EcsWaitTime = 30 * time.Second

type Ecs struct {
	client *ecs.Client
}

func NewEcs(cfg aws.Config) manager.Deployment {
	return &Ecs{ecs.NewFromConfig(cfg)}
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

func (e Ecs) UpdateService(cluster, service, sha string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Describe service to get task definition ARN.
	descSvcInput := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	descOutput, err := e.client.DescribeServices(ctx, descSvcInput)
	if err != nil {
		return "", fmt.Errorf("updateService: describe service error: %s, %s, %s, %w", cluster, service, sha, err)
	}

	// Describe task to get full task definition.
	taskDefArn := descOutput.Services[0].TaskDefinition
	descTaskInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: taskDefArn,
	}
	descTaskOutput, err := e.client.DescribeTaskDefinition(ctx, descTaskInput)
	if err != nil {
		return "", fmt.Errorf("updateService: describe service error: %s, %s, %s, %w", cluster, service, sha, err)
	}

	// Register a new task definition with an updated image.
	taskDef := descTaskOutput.TaskDefinition
	// TODO: Use proper repository path
	taskDef.ContainerDefinitions[0].Image = aws.String("ceramicnetwork/js-ceramic:" + sha)
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
		return "", fmt.Errorf("updateService: describe service error: %s, %s, %s, %w", cluster, service, sha, err)
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
		return "", fmt.Errorf("updateService: update service error: %s, %s, %s, %w", cluster, service, sha, err)
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
