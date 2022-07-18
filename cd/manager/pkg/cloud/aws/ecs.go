package aws

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

var _ cloud.Deployment = &Ecs{}

const EcsWaitTime = 30 * time.Second

type Ecs struct {
	client *ecs.Client
	env    string
}

func NewEcs(cfg aws.Config, env string) cloud.Deployment {
	return &Ecs{ecs.NewFromConfig(cfg), env}
}

func (e Ecs) UpdateService(service, cluster string) error {
	//ctx, cancel := context.WithTimeout(context.Background(), WaitTime)
	//defer cancel()
	//
	//newTd := &types.TaskDefinition{
	//
	//}
	//input := &ecs.UpdateServiceInput{
	//	Service: aws.String(service),
	//	Cluster: aws.String(cluster),
	//}
	//_, err := e.client.UpdateService(ctx, input)
	//if err != nil {
	//	return fmt.Errorf("ecs: update service error: %s, %s, %w", service, cluster, err)
	//}
	return nil
}

func (e Ecs) Launch(cluster, service, family string) error {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	in := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	op, err := e.client.DescribeServices(ctx, in)
	if err != nil {
		return fmt.Errorf("ecs: describe service error: %s, %s, %w", family, cluster, err)
	}
	log.Printf("ecs: describe service: %v", op)
	input := &ecs.RunTaskInput{
		TaskDefinition:       aws.String(family),
		Cluster:              aws.String(cluster),
		Count:                aws.Int32(1),
		LaunchType:           "FARGATE",
		NetworkConfiguration: op.Services[0].NetworkConfiguration,
		StartedBy:            aws.String("cd-manager"),
	}
	_, err = e.client.RunTask(ctx, input)
	if err != nil {
		return fmt.Errorf("ecs: update service error: %s, %s, %w", family, cluster, err)
	}
	return nil
}

func (e Ecs) RestartService(string, string) error {
	return nil
}
