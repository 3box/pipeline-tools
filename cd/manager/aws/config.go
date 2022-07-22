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
	endpointResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		if len(awsEndpoint) > 0 {
			log.Printf("Using custom endpoint: %s", awsEndpoint)
			return aws.Endpoint{
				PartitionID:   "aws",
				URL:           awsEndpoint,
				SigningRegion: os.Getenv("AWS_REGION"),
			}, nil
		}
		// Returning EndpointNotFoundError will allow the service to fallback to its default resolution.
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})
	return config.LoadDefaultConfig(context.TODO(), config.WithEndpointResolverWithOptions(endpointResolver))
}
