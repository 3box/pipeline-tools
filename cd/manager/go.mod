module github.com/3box/pipeline-tools/cd/manager

go 1.19

replace github.com/3box/pipeline-tools/cd/manager/common/aws/utils v0.0.0-20231026113921-2d40ca35ce75 => ./common/aws/utils

replace github.com/3box/pipeline-tools/cd/manager/common/job v0.0.0-20231026113921-2d40ca35ce75 => ./common/job

require (
	github.com/3box/pipeline-tools/cd/manager/common/aws/utils v0.0.0-20231026113921-2d40ca35ce75
	github.com/3box/pipeline-tools/cd/manager/common/job v0.0.0-20231026113921-2d40ca35ce75
	github.com/aws/aws-sdk-go-v2 v1.21.2
	github.com/aws/aws-sdk-go-v2/config v1.15.13
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.9.10
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.15.10
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.23.0
	github.com/aws/aws-sdk-go-v2/service/ecs v1.18.11
	github.com/aws/aws-sdk-go-v2/service/ssm v1.27.12
	github.com/disgoorg/disgo v0.13.16
	github.com/disgoorg/snowflake/v2 v2.0.0
	github.com/google/go-github v17.0.0+incompatible
	github.com/google/uuid v1.3.0
	github.com/joho/godotenv v1.4.0
	github.com/mitchellh/mapstructure v1.5.0
	golang.org/x/oauth2 v0.1.0
	golang.org/x/text v0.6.0
)

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.12.8 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.12.8 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.43 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.37 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.13.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.7.37 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.11.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.16.9 // indirect
	github.com/aws/smithy-go v1.15.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/disgoorg/log v1.2.0 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/sasha-s/go-csync v0.0.0-20210812194225-61421b77c44b // indirect
	github.com/stretchr/testify v1.7.2 // indirect
	golang.org/x/net v0.5.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
)
