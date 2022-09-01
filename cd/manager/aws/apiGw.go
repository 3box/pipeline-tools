package aws

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"

	"github.com/3box/pipeline-tools/cd/manager"
)

type Api struct {
	client *apigateway.Client
}

func NewApi(cfg aws.Config) manager.ApiGw {
	return &Api{apigateway.NewFromConfig(cfg)}
}

func (a *Api) Invoke(method, resourceId, restApiId, pathWithQueryString string) (string, error) {
	input := &apigateway.TestInvokeMethodInput{
		HttpMethod:          aws.String(method),
		ResourceId:          aws.String(resourceId),
		RestApiId:           aws.String(restApiId),
		PathWithQueryString: aws.String(pathWithQueryString),
	}
	output, err := a.client.TestInvokeMethod(context.TODO(), input)
	if (err != nil) || (output.Status != 200) {
		log.Printf("api: invocation failed: %v", err)
		return "", err
	}
	return *output.Body, nil
}
