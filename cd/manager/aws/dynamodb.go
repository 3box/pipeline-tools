package aws

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
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
	client   *dynamodb.Client
	table    string
	cache    manager.Cache
	tsCursor time.Time
}

func NewDynamoDb(cfg aws.Config, cache manager.Cache) manager.Database {
	tableName := os.Getenv("TABLE_NAME")
	db := &DynamoDb{
		dynamodb.NewFromConfig(cfg),
		tableName,
		cache,
		time.UnixMilli(0),
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
	// Load all jobs in an advanced stage of processing (completed, failed, started, waiting, skipped), so that we know which
	// jobs have already been dequeued.
	if err := db.loadJobs(manager.JobStage_Completed); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Failed); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Started); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Waiting); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Skipped); err != nil {
		return err
	}
	return nil
}

func (db DynamoDb) loadJobs(stage manager.JobStage) error {
	if err := db.iterateJobs(stage, func(jobState manager.JobState) bool {
		db.cache.WriteJob(jobState)
		// Return true so that we keep on iterating.
		return true
	}); err != nil {
		return err
	}
	return nil
}

func (db DynamoDb) QueueJob(jobState manager.JobState) error {
	// Only write this job to the database since that's where our de/queueing is expected to happen from. The cache is
	// just a hash-map from job IDs to job state.
	return db.writeJob(jobState)
}

func (db DynamoDb) DequeueJobs() []manager.JobState {
	jobs := make([]manager.JobState, 0, 0)
	if err := db.iterateJobs(manager.JobStage_Queued, func(jobState manager.JobState) bool {
		// If a job is not already in the cache, append it since it hasn't been dequeued yet.
		if _, found := db.cache.JobById(jobState.Id); !found {
			jobs = append(jobs, jobState)
		}
		// Return true so that we keep on iterating.
		return true
	}); err != nil {
		log.Printf("dequeueJobs: failed iteration through jobs: %v", err)
	}
	return jobs
}

func (db DynamoDb) iterateJobs(jobStage manager.JobStage, iter func(manager.JobState) bool) error {
	// If available, use the timestamp of the latest job to enter processing as the start of the database search. We
	// *know* that any subsequent jobs haven't yet been processed since we'll always process jobs in order, even if
	// multiple are processed simultaneously. Otherwise, look for jobs queued at most 1 day in the past.
	var rangeTs time.Time
	ttlTs := time.Now().AddDate(0, 0, -manager.DefaultTtlDays)
	if db.tsCursor.After(ttlTs) {
		rangeTs = db.tsCursor
	} else {
		rangeTs = ttlTs
	}
	p := dynamodb.NewQueryPaginator(db.client, &dynamodb.QueryInput{
		TableName:              aws.String(db.table),
		KeyConditionExpression: aws.String("#stage = :stage and #ts > :ts"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":stage": &types.AttributeValueMemberS{Value: string(jobStage)},
			":ts":    &types.AttributeValueMemberN{Value: strconv.FormatInt(rangeTs.UnixMilli(), 10)},
		},
		ExpressionAttributeNames: map[string]string{
			"#stage": "stage",
			"#ts":    "ts",
		},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(context.TODO())
		if err != nil {
			return err
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
			return fmt.Errorf("initialize: unable to unmarshal jobState: %v", err)
		}
		for _, jobState := range jobsPage {
			if !iter(jobState) {
				return nil
			}
		}
	}
	return nil
}

func (db DynamoDb) UpdateJob(jobState manager.JobState) error {
	// We might decide dequeue multiple, compatible jobs from the queue during processing, and if we set the timestamp
	// cursor to the timestamp of the last unprocessed job to be dequeued then decided not to process these jobs
	// (e.g. if a deployment was in progress), then the cursor could potentially miss one or more earlier jobs out of
	// the set of eligible jobs the next time we iterate.
	//
	// Instead we'll set the cursor to the timestamp of the last job to enter processing so that we *know* that any
	// subsequent jobs haven't yet been processed since we'll always process jobs in order, even if multiple are
	// processed simultaneously. Jobs that are entering processing will not be present in the cache before this point.
	if _, found := db.cache.JobById(jobState.Id); !found {
		// Since this function can be called from multiple goroutines simultaneously (for compatible jobs being
		// processed in parallel), make sure that the cursor is only moved forward.
		if jobState.Ts.After(db.tsCursor) {
			db.tsCursor = jobState.Ts
		}
	}
	if err := db.writeJob(jobState); err != nil {
		return err
	}
	db.cache.WriteJob(jobState)
	return nil
}

func (db DynamoDb) writeJob(jobState manager.JobState) error {
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
