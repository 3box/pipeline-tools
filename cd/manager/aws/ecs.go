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
		return "", fmt.Errorf("ecs: describe service error: %s, %s, %w", family, cluster, err)
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
		return "", fmt.Errorf("ecs: run task error: %s, %s, %v, %w", family, cluster, overrides, err)
	}
	return *output.Tasks[0].TaskArn, nil
}

func (e Ecs) CheckService(cluster string, taskArn ...string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	descInput := &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   taskArn,
	}
	descOutput, err := e.client.DescribeTasks(ctx, descInput)
	if err != nil {
		return false, fmt.Errorf("ecs: describe service error: %s, %s, %w", cluster, taskArn, err)
	}
	if (len(descOutput.Tasks) > 0) && (*descOutput.Tasks[0].LastStatus == string(types.DesiredStatusRunning)) {
		return true, nil
	}
	return false, nil
}

func (e Ecs) RestartService(string, string) error {
	return nil
}

func (e Ecs) UpdateService(string, string) error {
	return nil
}
