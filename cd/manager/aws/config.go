package aws

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

func Config() (aws.Config, error) {
	awsEndpoint := os.Getenv("AWS_ENDPOINT")
	awsRegion := os.Getenv("AWS_REGION")
	if len(awsEndpoint) > 0 {
		log.Printf("Using custom aws endpoint: %s", awsEndpoint)
		endpointResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				PartitionID:   "aws",
				URL:           awsEndpoint,
				SigningRegion: awsRegion,
			}, nil
		})
		return config.LoadDefaultConfig(context.TODO(), config.WithEndpointResolverWithOptions(endpointResolver))
	}
	return config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsRegion))
}
