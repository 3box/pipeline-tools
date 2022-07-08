package aws

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud"
)

var _ cloud.Database = &DynamoDb{}

const StateEntryName = "cd"

type DynamoDb struct {
	client *dynamodb.Client
	table  *string
}

type DbState struct {
	State map[string]*types.AttributeValue `dynamodbav:"state" json:"state"`
}

func NewDynamoDb(cfg aws.Config, env string) cloud.Database {
	table := "ceramic-" + env + "-ops"
	return &DynamoDb{dynamodb.NewFromConfig(cfg), &table}
}

func (db DynamoDb) GetState(ctx context.Context) (*cloud.State, error) {
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
	return &cloud.State{}, nil
}

func (db DynamoDb) UpdateState(ctx context.Context, state *cloud.State) error {
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
