package apigw

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"

	"github.com/3box/pipeline-tools/cd/manager"
)

type ApiGw struct {
	client *apigateway.Client
}

func NewApiGw(cfg aws.Config) manager.ApiGw {
	return &ApiGw{apigateway.NewFromConfig(cfg)}
}

func (a *ApiGw) Invoke(method, resourceId, restApiId, pathWithQueryString string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	input := &apigateway.TestInvokeMethodInput{
		HttpMethod:          aws.String(method),
		ResourceId:          aws.String(resourceId),
		RestApiId:           aws.String(restApiId),
		PathWithQueryString: aws.String(pathWithQueryString),
	}
	output, err := a.client.TestInvokeMethod(ctx, input)
	if (err != nil) || (output.Status != 200) {
		log.Printf("api: invocation failed: %v", err)
		return "", err
	}
	return *output.Body, nil
}
