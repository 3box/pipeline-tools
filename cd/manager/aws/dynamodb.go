package aws

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Database = &DynamoDb{}

const StateEntryName = "cd"

type DynamoDb struct {
	client *dynamodb.Client
	table  *string
}

type DbState struct {
	State map[string]*types.AttributeValue `dynamodbav:"state" json:"state"`
}

func NewDynamoDb(cfg aws.Config) manager.Database {
	tableName := os.Getenv("TABLE_NAME")
	db := &DynamoDb{dynamodb.NewFromConfig(cfg), &tableName}
	// This block should only be needed when injecting a custom AWS endpoint (usually when testing locally).
	awsEndpoint := os.Getenv("AWS_ENDPOINT")
	if len(awsEndpoint) > 0 {
		_, err := db.client.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{TableName: aws.String(tableName)})
		if err != nil {
			in := dynamodb.CreateTableInput{
				AttributeDefinitions: []types.AttributeDefinition{{
					AttributeName: aws.String("name"),
					AttributeType: "S",
				}},
				KeySchema: []types.KeySchemaElement{{
					AttributeName: aws.String("name"),
					KeyType:       "HASH",
				}},
				TableName: aws.String(tableName),
				ProvisionedThroughput: &types.ProvisionedThroughput{
					ReadCapacityUnits:  aws.Int64(1),
					WriteCapacityUnits: aws.Int64(1),
				},
			}
			if _, err = db.client.CreateTable(context.Background(), &in); err != nil {
				log.Printf("dynamodb: table creation failed: %v", err)
			}
		}
	}
	return db
}

func (db DynamoDb) GetState(ctx context.Context) (*manager.State, error) {
	result, err := db.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: db.table,
		Key: map[string]types.AttributeValue{
			"name": &types.AttributeValueMemberS{Value: StateEntryName},
		},
	})
	if err != nil {
		return nil, err
	}

	if result.Item == nil {
		return nil, errors.New("could not find state entry")
	}

	//state := DbState{}

	//err = dynamodbattribute.UnmarshalMap(result.Item, &state)
	//if err != nil {
	//	return nil, err
	//}
	//log.Printf("state: %v", state)
	return &manager.State{}, nil
}

func (db DynamoDb) UpdateState(ctx context.Context, state *manager.State) error {
	//s := DbState{}
	input := &dynamodb.UpdateItemInput{
		ExpressionAttributeValues: map[string]types.AttributeValue{
			"state": &types.AttributeValueMemberM{
				Value: map[string]types.AttributeValue{
					"id": &types.AttributeValueMemberS{Value: state.Id},
				},
			},
		},
		TableName: db.table,
		Key: map[string]types.AttributeValue{
			"name": &types.AttributeValueMemberS{Value: StateEntryName},
		},
		ReturnValues:     types.ReturnValueAllNew,
		UpdateExpression: aws.String("set state = :s"),
	}
	_, err := db.client.UpdateItem(ctx, input)
	if err != nil {
		return err
	}

	return nil
}
