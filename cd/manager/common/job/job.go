package job

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/3box/pipeline-tools/cd/manager/common/aws/utils"
)

func IsFinishedJob(jobState JobState) bool {
	return (jobState.Stage == JobStage_Skipped) || (jobState.Stage == JobStage_Canceled) || (jobState.Stage == JobStage_Failed) || (jobState.Stage == JobStage_Completed)
}

func IsActiveJob(jobState JobState) bool {
	return (jobState.Stage == JobStage_Started) || (jobState.Stage == JobStage_Waiting)
}

func IsTimedOut(jobState JobState, delay time.Duration) bool {
	// If no timestamp was stored, use the timestamp from the last update.
	startTime := jobState.Ts
	if s, found := jobState.Params[JobParam_Start].(float64); found {
		startTime = time.Unix(0, int64(s))
	}
	return time.Now().Add(-delay).After(startTime)
}

func CreateJobTable(ctx context.Context, client *dynamodb.Client, table string) error {
	createTableInput := dynamodb.CreateTableInput{
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("id"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("job"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("stage"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("type"),
				AttributeType: "S",
			},
			{
				AttributeName: aws.String("ts"),
				AttributeType: "N",
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("id"),
				KeyType:       "HASH",
			},
		},
		TableName: aws.String(table),
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String(StageTsIndex),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("stage"),
						KeyType:       "HASH",
					},
					{
						AttributeName: aws.String("ts"),
						KeyType:       "RANGE",
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll,
				},
			},
			{
				IndexName: aws.String(TypeTsIndex),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("type"),
						KeyType:       "HASH",
					},
					{
						AttributeName: aws.String("ts"),
						KeyType:       "RANGE",
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll,
				},
			},
			{
				IndexName: aws.String(JobTsIndex),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("job"),
						KeyType:       "HASH",
					},
					{
						AttributeName: aws.String("ts"),
						KeyType:       "RANGE",
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll,
				},
			},
		},
	}
	return utils.CreateTable(ctx, client, &createTableInput)
}

func CreateWorkflowJob(jobState JobState) (Workflow, error) {
	if org, found := jobState.Params[WorkflowJobParam_Org].(string); !found {
		return Workflow{}, fmt.Errorf("missing org")
	} else if repo, found := jobState.Params[WorkflowJobParam_Repo].(string); !found {
		return Workflow{}, fmt.Errorf("missing repo")
	} else if ref, found := jobState.Params[WorkflowJobParam_Ref].(string); !found {
		return Workflow{}, fmt.Errorf("missing ref")
	} else if workflow, found := jobState.Params[WorkflowJobParam_Workflow].(string); !found {
		return Workflow{}, fmt.Errorf("missing workflow")
	} else {
		workflowInputs := make(map[string]interface{}, 0)
		if inputs, found := jobState.Params[WorkflowJobParam_Inputs].(map[string]interface{}); found {
			workflowInputs = inputs
		}
		workflowRunUrl, _ := jobState.Params[WorkflowJobParam_Url].(string)
		var workflowRunId int64 = 0
		if id, found := jobState.Params[JobParam_Id].(float64); found {
			workflowRunId = int64(id)
		}
		return Workflow{org, repo, workflow, ref, workflowInputs, workflowRunUrl, workflowRunId}, nil
	}
}
