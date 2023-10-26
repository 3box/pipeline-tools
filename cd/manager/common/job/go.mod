module github.com/3box/pipeline-tools/cd/manager/common/job

go 1.20

replace github.com/3box/pipeline-tools/cd/manager/common/aws/utils v0.0.0 => ../aws/utils

require (
	github.com/3box/pipeline-tools/cd/manager/common/aws/utils v0.0.0
	github.com/aws/aws-sdk-go-v2 v1.21.2
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.23.0
)

require (
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.43 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.37 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.7.37 // indirect
	github.com/aws/smithy-go v1.15.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
)
