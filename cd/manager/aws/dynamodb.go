package aws

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/3box/pipeline-tools/cd/manager"
)

const TableCreationRetries = 3
const TableCreationWait = 3 * time.Second

var _ manager.Database = &DynamoDb{}

type DynamoDb struct {
	client *dynamodb.Client
	table  string
	jobs   *sync.Map
}

func NewDynamoDb(cfg aws.Config) manager.Database {
	tableName := os.Getenv("TABLE_NAME")
	db := &DynamoDb{
		dynamodb.NewFromConfig(cfg),
		tableName,
		new(sync.Map),
	}
	if err := db.createTable(); err != nil {
		log.Fatalf("dynamodb: table creation failed: %v", err)
	}
	return db
}

func (db DynamoDb) createTable() error {
	// Create the table if it doesn't already exist
	_, err := db.client.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{TableName: aws.String(db.table)})
	if err != nil {
		in := dynamodb.CreateTableInput{
			AttributeDefinitions: []types.AttributeDefinition{
				{
					AttributeName: aws.String("stage"),
					AttributeType: "S",
				},
				{
					AttributeName: aws.String("ts"),
					AttributeType: "N",
				},
			},
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
			TableName: aws.String(db.table),
			ProvisionedThroughput: &types.ProvisionedThroughput{
				ReadCapacityUnits:  aws.Int64(1),
				WriteCapacityUnits: aws.Int64(1),
			},
		}
		if _, err = db.client.CreateTable(context.Background(), &in); err != nil {
			log.Fatalf("dynamodb: table creation failed: %v", err)
		}
		for i := 0; i < TableCreationRetries; i++ {
			describe, err := db.client.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{TableName: aws.String(db.table)})
			if (err == nil) && (describe.Table.TableStatus == types.TableStatusActive) {
				return nil
			}
			time.Sleep(TableCreationWait)
		}
		log.Fatalf("dynamodb: table creation failed: %v", err)
	}
	return nil
}

func (db DynamoDb) InitializeJobs() error {
	// Load completed/failed and processing jobs, in that order, so that we know which jobs have already been dequeued.
	if err := db.loadJobs(manager.JobStage_Completed); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Failed); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Processing); err != nil {
		return err
	}
	return nil
}

func (db DynamoDb) loadJobs(stage manager.JobStage) error {
	if _, err := db.iterateJobs(stage, func(jobState *manager.JobState) *manager.JobState {
		// Only cache job if it wasn't already cached
		if db.JobById(jobState.Id) == nil {
			db.writeJobToCache(jobState)
		}
		// Return nil so that we keep on iterating.
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (db DynamoDb) QueueJob(jobState *manager.JobState) error {
	return db.writeJobToDb(jobState)
}

func (db DynamoDb) DequeueJob() (*manager.JobState, error) {
	return db.iterateJobs(manager.JobStage_Queued, func(jobState *manager.JobState) *manager.JobState {
		// If a job is not already in the cache, return it since it hasn't been dequeued yet. This will also terminate
		// iteration.
		if db.JobById(jobState.Id) == nil {
			return jobState
		}
		// Return nil so that we keep on iterating.
		return nil
	})
}

func (db DynamoDb) iterateJobs(jobStage manager.JobStage, iter func(*manager.JobState) *manager.JobState) (*manager.JobState, error) {
	oldestTs := time.Now().AddDate(0, 0, -manager.DefaultTtlDays).UnixMilli()
	p := dynamodb.NewQueryPaginator(db.client, &dynamodb.QueryInput{
		TableName:              aws.String(db.table),
		KeyConditionExpression: aws.String("#stage = :stage and #ts > :ts"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":stage": &types.AttributeValueMemberS{Value: string(jobStage)},
			":ts":    &types.AttributeValueMemberN{Value: strconv.FormatInt(oldestTs, 10)},
		},
		ExpressionAttributeNames: map[string]string{
			"#stage": "stage",
			"#ts":    "ts",
		},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(context.TODO())
		if err != nil {
			return nil, err
		}
		var jobsPage []manager.JobState
		tsDecode := func(ts string) (time.Time, error) {
			msec, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.UnixMilli(msec), nil
		}
		err = attributevalue.UnmarshalListOfMapsWithOptions(page.Items, &jobsPage, func(options *attributevalue.DecoderOptions) {
			options.DecodeTime = attributevalue.DecodeTimeAttributes{
				S: tsDecode,
				N: tsDecode,
			}
		})
		if err != nil {
			return nil, fmt.Errorf("initialize: unable to unmarshal jobState: %v", err)
		}
		for _, jobState := range jobsPage {
			if js := iter(&jobState); js != nil {
				return js, nil
			}
		}
	}
	return nil, nil
}

func (db DynamoDb) UpdateJob(jobState *manager.JobState) error {
	if err := db.writeJobToDb(jobState); err != nil {
		return err
	}
	db.writeJobToCache(jobState)
	return nil
}

func (db DynamoDb) writeJobToDb(jobState *manager.JobState) error {
	if attributeValues, err := attributevalue.MarshalMapWithOptions(jobState, func(options *attributevalue.EncoderOptions) {
		options.EncodeTime = func(time time.Time) (types.AttributeValue, error) {
			return &types.AttributeValueMemberN{Value: strconv.FormatInt(time.UnixMilli(), 10)}, nil
		}
	}); err != nil {
		return err
	} else if _, err = db.client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(db.table),
		Item:      attributeValues,
	}); err != nil {
		return err
	}
	return nil
}

func (db DynamoDb) writeJobToCache(jobState *manager.JobState) {
	// Don't overwrite an advanced state with an earlier state.
	if foundJobState := db.JobById(jobState.Id); foundJobState != nil {
		if jobState.Stage == manager.JobStage_Queued {
			if foundJobState.Stage == manager.JobStage_Processing || foundJobState.Stage == manager.JobStage_Failed || foundJobState.Stage == manager.JobStage_Completed {
				return
			}
		} else if jobState.Stage == manager.JobStage_Processing {
			if foundJobState.Stage == manager.JobStage_Failed || foundJobState.Stage == manager.JobStage_Completed {
				return
			}
		}
		// We've reached one of the two terminal states, so there's no need to overwrite this entry.
		return
	}
	db.jobs.Store(jobState.Id, jobState)
}

func (db DynamoDb) DeleteJob(jobState *manager.JobState) error {
	// Never delete jobs from the database, but here's the code to do so.
	//_, err := db.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
	//	TableName: aws.String(db.table),
	//	Key: map[string]types.AttributeValue{
	//		"state": &types.AttributeValueMemberS{Value: string(jobState.Stage)},
	//		"ts":    &types.AttributeValueMemberN{Value: strconv.FormatInt(jobState.Ts.UnixMilli(), 10)},
	//	},
	//})
	//if err != nil {
	//	return err
	//}

	// Delete job from cache
	db.jobs.Delete(jobState.Id)
	return nil
}

func (db DynamoDb) JobById(id string) *manager.JobState {
	if job, found := db.jobs.Load(id); found {
		return job.(*manager.JobState)
	}
	return nil
}

func (db DynamoDb) JobsByStage(jobStage manager.JobStage, jobTypes ...manager.JobType) map[string]*manager.JobState {
	jobs := make(map[string]*manager.JobState)
	db.jobs.Range(func(_, value interface{}) bool {
		jobState := value.(*manager.JobState)
		if jobState.Stage == jobStage {
			matched := false
			if len(jobTypes) > 0 {
				for _, jobType := range jobTypes {
					if jobState.Type == jobType {
						matched = true
						break
					}
				}
			} else {
				matched = true
			}
			if matched {
				jobs[jobState.Id] = jobState
			}
		}
		return true
	})
	return jobs
}
