package aws

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud"
)

type Api struct {
	client *apigateway.Client
}

func NewApi(cfg aws.Config) cloud.ApiGw {
	log.Printf("api client: %s", cfg.HTTPClient)
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
		return "", fmt.Errorf("api: invocation failed: %s", err)
	}
	return *output.Body, nil
}
