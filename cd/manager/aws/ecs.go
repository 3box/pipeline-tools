package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/3box/pipeline-tools/cd/manager"
)

const EcsWaitTime = 30 * time.Second

var _ manager.Deployment = &Ecs{}

type Ecs struct {
	ecsClient *ecs.Client
	ssmClient *ssm.Client
	env       manager.EnvType
}

type ecsFailure struct {
	arn, detail, reason string
}

func NewEcs(cfg aws.Config) manager.Deployment {
	return &Ecs{ecs.NewFromConfig(cfg), ssm.NewFromConfig(cfg), manager.EnvType(os.Getenv("ENV"))}
}

func (e Ecs) LaunchService(cluster, service, family, container string, overrides map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	output, err := e.describeService(ctx, cluster, service)
	if err != nil {
		return "", err
	}
	return e.runTask(ctx, cluster, family, container, output.Services[0].NetworkConfiguration, overrides)
}

func (e Ecs) LaunchTask(cluster, family, container, vpcConfigParam string, overrides map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Get the VPC configuration from SSM
	input := &ssm.GetParameterInput{
		Name:           aws.String(vpcConfigParam),
		WithDecryption: false,
	}
	output, err := e.ssmClient.GetParameter(ctx, input)
	if err != nil {
		log.Printf("ecs: get vpc config error: %s, %s, %s, %+v, %v", cluster, family, vpcConfigParam, overrides, err)
		return "", err
	}
	var vpcConfig types.AwsVpcConfiguration
	if err = json.Unmarshal([]byte(*output.Parameter.Value), &vpcConfig); err != nil {
		log.Printf("launchTask: error unmarshaling worker network configuration: %s, %s, %s, %+v, %v", cluster, family, vpcConfigParam, overrides, err)
		return "", err
	}
	return e.runTask(ctx, cluster, family, container, &types.NetworkConfiguration{AwsvpcConfiguration: &vpcConfig}, overrides)
}

func (e Ecs) CheckTask(running bool, cluster string, taskArn ...string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Describe cluster tasks matching the specified ARNs.
	input := &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   taskArn,
	}
	output, err := e.ecsClient.DescribeTasks(ctx, input)
	if err != nil {
		log.Printf("checkTask: describe service error: %s, %s, %v", cluster, taskArn, err)
		return false, err
	}
	var checkStatus types.DesiredStatus
	if running {
		checkStatus = types.DesiredStatusRunning
	} else {
		checkStatus = types.DesiredStatusStopped
	}
	// Check whether the specified tasks are running.
	if len(output.Tasks) > 0 {
		// We found one or more tasks, only return true if all specified tasks were in the right state.
		for _, task := range output.Tasks {
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
	descSvcOutput, err := e.describeService(ctx, cluster, service)

	// Describe task to get full task definition.
	newTaskDefArn, err := e.updateTaskDefinition(ctx, *descSvcOutput.Services[0].TaskDefinition, image)

	// Update the service to use the new task definition.
	updateSvcInput := &ecs.UpdateServiceInput{
		Service:              aws.String(service),
		Cluster:              aws.String(cluster),
		DesiredCount:         aws.Int32(1),
		EnableExecuteCommand: aws.Bool(true),
		ForceNewDeployment:   false,
		TaskDefinition:       aws.String(newTaskDefArn),
	}
	_, err = e.ecsClient.UpdateService(ctx, updateSvcInput)
	if err != nil {
		log.Printf("updateService: update service error: %s, %s, %s, %v", cluster, service, image, err)
		return "", err
	}
	return newTaskDefArn, nil
}

func (e Ecs) UpdateTask(family, image string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Get the latest task definition ARN.
	input := &ecs.ListTaskDefinitionsInput{
		FamilyPrefix: aws.String(family),
		MaxResults:   aws.Int32(1),
		Sort:         types.SortOrderDesc,
	}
	output, err := e.ecsClient.ListTaskDefinitions(ctx, input)
	if err != nil {
		return "", err
	}
	return e.updateTaskDefinition(ctx, output.TaskDefinitionArns[0], image)
}

func (e Ecs) CheckService(cluster, service, taskDefArn string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), EcsWaitTime)
	defer cancel()

	// Describe service to get deployment status
	descSvcInput := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	descOutput, err := e.ecsClient.DescribeServices(ctx, descSvcInput)
	if err != nil {
		log.Printf("checkService: describe service error: %s, %s, %s, %v", cluster, service, taskDefArn, err)
		return false, err
	}
	if len(descOutput.Failures) > 0 {
		ecsFailures := parseEcsFailures(descOutput.Failures)
		log.Printf("checkService: describe service error: %s, %s, %s, %v", cluster, service, taskDefArn, ecsFailures)
		return false, fmt.Errorf("%v", ecsFailures)
	}

	// Look for deployments using the new task definition with at least 1 running task.
	for _, deployment := range descOutput.Services[0].Deployments {
		if (*deployment.TaskDefinition == taskDefArn) && (deployment.RunningCount > 0) {
			return true, nil
		}
	}
	return false, nil
}

func (e Ecs) PopulateLayout(component manager.DeployComponent) (map[string]interface{}, error) {
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

	var privateLayout map[manager.DeployType]interface{}
	var publicLayout map[manager.DeployType]interface{}
	var casLayout map[manager.DeployType]interface{}
	switch component {
	case manager.DeployComponent_Ceramic:
		privateLayout = map[manager.DeployType]interface{}{
			manager.DeployType_Service: map[string]interface{}{
				privateCluster + "-" + ServiceSuffix_CeramicNode: nil,
			},
		}
		publicLayout = map[manager.DeployType]interface{}{
			manager.DeployType_Service: map[string]interface{}{
				publicCluster + "-" + ServiceSuffix_CeramicNode:    nil,
				publicCluster + "-" + ServiceSuffix_CeramicGateway: nil,
			},
		}
		if e.env == manager.EnvType_Prod {
			publicLayout[manager.DeployType_Service].(map[string]interface{})[globalPrefix+"-"+ServiceSuffix_Elp11CeramicNode] = nil
			publicLayout[manager.DeployType_Service].(map[string]interface{})[globalPrefix+"-"+ServiceSuffix_Elp12CeramicNode] = nil
		}
		casLayout = map[manager.DeployType]interface{}{
			manager.DeployType_Service: map[string]interface{}{
				casCluster + "-" + ServiceSuffix_CeramicNode: nil,
			},
		}
	case manager.DeployComponent_Ipfs:
		privateLayout = map[manager.DeployType]interface{}{
			manager.DeployType_Service: map[string]interface{}{
				privateCluster + "-" + ServiceSuffix_IpfsNode: nil,
			},
		}
		publicLayout = map[manager.DeployType]interface{}{
			manager.DeployType_Service: map[string]interface{}{
				publicCluster + "-" + ServiceSuffix_IpfsNode:    nil,
				publicCluster + "-" + ServiceSuffix_IpfsGateway: nil,
			},
		}
		if e.env == manager.EnvType_Prod {
			publicLayout[manager.DeployType_Service].(map[string]interface{})[globalPrefix+"-"+ServiceSuffix_Elp11IpfsNode] = nil
			publicLayout[manager.DeployType_Service].(map[string]interface{})[globalPrefix+"-"+ServiceSuffix_Elp12IpfsNode] = nil
		}
		casLayout = map[manager.DeployType]interface{}{
			manager.DeployType_Service: map[string]interface{}{
				casCluster + "-" + ServiceSuffix_IpfsNode: nil,
			},
		}
	case manager.DeployComponent_Cas:
		casLayout = map[manager.DeployType]interface{}{
			manager.DeployType_Service: map[string]interface{}{
				casCluster + "-" + ServiceSuffix_CasApi: nil,
			},
			manager.DeployType_Task: map[string]interface{}{
				casCluster + "-" + ServiceSuffix_CasAnchor: nil,
			},
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

func (e Ecs) GetRegistryUri(component manager.DeployComponent) (string, error) {
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

func (e Ecs) describeService(ctx context.Context, cluster, service string) (*ecs.DescribeServicesOutput, error) {
	input := &ecs.DescribeServicesInput{
		Services: []string{service},
		Cluster:  aws.String(cluster),
	}
	output, err := e.ecsClient.DescribeServices(ctx, input)
	if err != nil {
		log.Printf("describeService: %s, %s, %v", service, cluster, err)
		return nil, err
	}
	if len(output.Failures) > 0 {
		ecsFailures := parseEcsFailures(output.Failures)
		log.Printf("describeService: failure: %s, %s, %v", service, cluster, ecsFailures)
		return nil, fmt.Errorf("%v", ecsFailures)
	}
	return output, nil
}

func (e Ecs) runTask(ctx context.Context, cluster, family, container string, networkConfig *types.NetworkConfiguration, overrides map[string]string) (string, error) {
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
	output, err := e.ecsClient.RunTask(ctx, input)
	if err != nil {
		log.Printf("runTask: %s, %s, %s, %+v, %v", cluster, family, container, overrides, err)
		return "", err
	}
	return *output.Tasks[0].TaskArn, nil
}

func (e Ecs) updateTaskDefinition(ctx context.Context, taskDefArn, image string) (string, error) {
	descTaskDefInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}
	descTaskDefOutput, err := e.ecsClient.DescribeTaskDefinition(ctx, descTaskDefInput)
	if err != nil {
		log.Printf("updateTaskDefinition: describe task def error: %s, %s, %v", taskDefArn, image, err)
		return "", err
	}
	// Register a new task definition with an updated image.
	taskDef := descTaskDefOutput.TaskDefinition
	taskDef.ContainerDefinitions[0].Image = aws.String(image)
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
		log.Printf("updateTaskDefinition: register task def error: %s, %s, %v", taskDefArn, image, err)
		return "", err
	}
	return *regTaskDefOutput.TaskDefinition.TaskDefinitionArn, nil
}

func parseEcsFailures(ecsFailures []types.Failure) []ecsFailure {
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
