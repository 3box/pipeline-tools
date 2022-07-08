package aws

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

func Config() (aws.Config, string, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}
	stsSvc := sts.NewFromConfig(cfg)
	input := &sts.GetCallerIdentityInput{}
	output, err := stsSvc.GetCallerIdentity(context.Background(), input)
	if err != nil {
		log.Fatalf("aws: failed to call STS service")
	}
	return cfg, *output.Account, nil
}
