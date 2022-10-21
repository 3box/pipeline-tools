package aws

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/mitchellh/mapstructure"

	"github.com/3box/pipeline-tools/cd/manager"
)

const TableCreationRetries = 3
const TableCreationWait = 3 * time.Second

var _ manager.Database = &DynamoDb{}

type DynamoDb struct {
	client     *dynamodb.Client
	jobTable   string
	buildTable string
	cache      manager.Cache
	tsCursor   time.Time
}

func NewDynamoDb(cfg aws.Config, cache manager.Cache) manager.Database {
	env := os.Getenv("ENV")
	// Use override endpoint, if specified, so that we can store jobs locally, while hitting regular AWS endpoints for
	// other operations. This allows local testing without affecting CD manager instances running in AWS.
	customEndpoint := os.Getenv("DB_AWS_ENDPOINT")
	var err error
	if len(customEndpoint) > 0 {
		log.Printf("newDynamoDb: using custom dynamodb aws endpoint: %s", customEndpoint)
		cfg, err = ConfigWithOverride(customEndpoint)
		if err != nil {
			log.Fatalf("Failed to create AWS cfg: %q", err)
		}
	}
	jobTable := "ceramic-" + env + "-ops"
	buildTable := "ceramic-utils-" + env
	db := &DynamoDb{
		dynamodb.NewFromConfig(cfg),
		jobTable,
		buildTable,
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
	if exists, err := db.tableExists(db.jobTable); !exists {
		ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
		defer cancel()

		createTableInput := dynamodb.CreateTableInput{
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
			TableName: aws.String(db.jobTable),
			ProvisionedThroughput: &types.ProvisionedThroughput{
				ReadCapacityUnits:  aws.Int64(1),
				WriteCapacityUnits: aws.Int64(1),
			},
		}
		if _, err = db.client.CreateTable(ctx, &createTableInput); err != nil {
			return err
		}
		var exists bool
		for i := 0; i < TableCreationRetries; i++ {
			if exists, err = db.tableExists(db.jobTable); exists {
				return nil
			}
			time.Sleep(TableCreationWait)
		}
		return err
	}
	return nil
}

func (db DynamoDb) tableExists(table string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	if output, err := db.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}); err != nil {
		return false, err
	} else {
		return output.Table.TableStatus == types.TableStatusActive, nil
	}
}

func (db DynamoDb) InitializeJobs() error {
	// Load all jobs in an advanced stage of processing (completed, failed, delayed, waiting, started, skipped), so that
	// we know which jobs have already been dequeued.
	if err := db.loadJobs(manager.JobStage_Completed); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Failed); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Canceled); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Delayed); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Waiting); err != nil {
		return err
	} else if err = db.loadJobs(manager.JobStage_Started); err != nil {
		return err
	} else {
		return db.loadJobs(manager.JobStage_Skipped)
	}
}

func (db DynamoDb) loadJobs(stage manager.JobStage) error {
	return db.iterateJobs(stage, func(jobState manager.JobState) bool {
		// Write loaded jobs to the cache
		db.cache.WriteJob(jobState)
		// Return true so that we keep on iterating
		return true
	})
}

func (db DynamoDb) QueueJob(jobState manager.JobState) error {
	// Only write this job to the database since that's where our de/queueing is expected to happen from. The cache is
	// just a hash-map from job IDs to job state for active jobs. Queued-but-not-started jobs are not added to the cache
	// until they are in progress.
	return db.WriteJob(jobState)
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
	// Only look for jobs up till the current time. This allows us to schedule jobs in the future (e.g. smoke tests to
	// start a few minutes after a deployment is complete).
	p := dynamodb.NewQueryPaginator(db.client, &dynamodb.QueryInput{
		TableName:              aws.String(db.jobTable),
		KeyConditionExpression: aws.String("#stage = :stage and #ts between :ts and :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":stage": &types.AttributeValueMemberS{Value: string(jobStage)},
			":ts":    &types.AttributeValueMemberN{Value: strconv.FormatInt(rangeTs.UnixMilli(), 10)},
			":now":   &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().UnixMilli(), 10)},
		},
		ExpressionAttributeNames: map[string]string{
			"#stage": "stage",
			"#ts":    "ts",
		},
	})
	for p.HasMorePages() {
		err := func() error {
			ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
			defer cancel()

			page, err := p.NextPage(ctx)
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
				log.Printf("initialize: unable to unmarshal jobState: %v", err)
				return err
			}
			for _, jobState := range jobsPage {
				if jobState.Type == manager.JobType_Deploy {
					// Marshal layout back into `Layout` structure
					if layout, found := jobState.Params[manager.JobParam_Layout].(map[string]interface{}); found {
						var marshaledLayout manager.Layout
						if err = mapstructure.Decode(layout, &marshaledLayout); err != nil {
							return err
						}
						jobState.Params[manager.JobParam_Layout] = marshaledLayout
					}
				}
				if !iter(jobState) {
					return nil
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (db DynamoDb) AdvanceJob(jobState manager.JobState) error {
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
	// Update the timestamp
	jobState.Ts = time.Now()
	if err := db.WriteJob(jobState); err != nil {
		return err
	}
	db.cache.WriteJob(jobState)
	return nil
}

func (db DynamoDb) WriteJob(jobState manager.JobState) error {
	if attributeValues, err := attributevalue.MarshalMapWithOptions(jobState, func(options *attributevalue.EncoderOptions) {
		options.EncodeTime = func(time time.Time) (types.AttributeValue, error) {
			return &types.AttributeValueMemberN{Value: strconv.FormatInt(time.UnixMilli(), 10)}, nil
		}
	}); err != nil {
		return err
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
		defer cancel()

		_, err = db.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(db.jobTable),
			Item:      attributeValues,
		})
		return err
	}
}

func (db DynamoDb) UpdateBuildHash(component manager.DeployComponent, sha string) error {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	_, err := db.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(db.buildTable),
		Key: map[string]types.AttributeValue{
			"key": &types.AttributeValueMemberS{Value: string(component)},
		},
		UpdateExpression: aws.String("set #buildInfo.#shaTag = :sha"),
		ExpressionAttributeNames: map[string]string{
			"#buildInfo": "buildInfo",
			"#shaTag":    "sha_tag",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":sha": &types.AttributeValueMemberS{Value: sha},
		},
	})
	return err
}

func (db DynamoDb) UpdateDeployHash(component manager.DeployComponent, sha string) error {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	_, err := db.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(db.buildTable),
		Key: map[string]types.AttributeValue{
			"key": &types.AttributeValueMemberS{Value: string(component)},
		},
		UpdateExpression: aws.String("set #deployTag = :sha"),
		ExpressionAttributeNames: map[string]string{
			"#deployTag": "deployTag",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":sha": &types.AttributeValueMemberS{Value: sha},
		},
	})
	return err
}

func (db DynamoDb) GetBuildHashes() (map[manager.DeployComponent]string, error) {
	if buildStates, err := db.getBuildStates(); err != nil {
		return nil, err
	} else {
		commitHashes := make(map[manager.DeployComponent]string, len(buildStates))
		for _, state := range buildStates {
			commitHashes[state.Key] = state.BuildInfo[manager.BuildHashTag].(string)
		}
		return commitHashes, nil
	}
}

func (db DynamoDb) GetDeployHashes() (map[manager.DeployComponent]string, error) {
	if buildStates, err := db.getBuildStates(); err != nil {
		return nil, err
	} else {
		commitHashes := make(map[manager.DeployComponent]string, len(buildStates))
		for _, state := range buildStates {
			commitHashes[state.Key] = state.DeployTag
		}
		return commitHashes, nil
	}
}

func (db DynamoDb) getBuildStates() ([]manager.BuildState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
	defer cancel()

	// We don't need to paginate since we're only ever going to have a handful of components.
	if scanOutput, err := db.client.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(db.buildTable),
	}); err != nil {
		return nil, err
	} else {
		var buildStates []manager.BuildState
		if err = attributevalue.UnmarshalListOfMapsWithOptions(scanOutput.Items, &buildStates); err != nil {
			return nil, err
		}
		return buildStates, nil
	}
}
