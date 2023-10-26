package ddb

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mitchellh/mapstructure"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/aws/config"
	"github.com/3box/pipeline-tools/cd/manager/common/aws/utils"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

var _ manager.Database = &DynamoDb{}

type DynamoDb struct {
	client     *dynamodb.Client
	jobTable   string
	buildTable string
	cache      manager.Cache
	cursor     time.Time
}

func NewDynamoDb(cfg aws.Config, cache manager.Cache) manager.Database {
	env := os.Getenv("ENV")
	// Use override endpoint, if specified, so that we can store jobs locally, while hitting regular AWS endpoints for
	// other operations. This allows local testing without affecting CD manager instances running in AWS.
	customEndpoint := os.Getenv("DB_AWS_ENDPOINT")
	var err error
	if len(customEndpoint) > 0 {
		log.Printf("newDynamoDb: using custom dynamodb aws endpoint: %s", customEndpoint)
		cfg, err = config.ConfigWithOverride(customEndpoint)
		if err != nil {
			log.Fatalf("Failed to create AWS cfg: %q", err)
		}
	}
	jobTable := "ceramic-" + env + "-ops"
	buildTable := "ceramic-utils-" + env
	dynamoDbClient := dynamodb.NewFromConfig(cfg)
	db := &DynamoDb{
		dynamoDbClient,
		jobTable,
		buildTable,
		cache,
		time.Unix(0, 0),
	}
	if err = db.createJobTable(); err != nil {
		log.Fatalf("dynamodb: job table creation failed: %v", err)
	}
	if err = db.createBuildTable(); err != nil {
		log.Fatalf("dynamodb: build table creation failed: %v", err)
	}
	return db
}

func (db DynamoDb) createJobTable() error {
	return job.CreateJobTable(context.Background(), db.client, db.jobTable)
}

func (db DynamoDb) createBuildTable() error {
	// Create the table if it doesn't already exist
	createTableInput := dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("key"),
				AttributeType: "S",
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("key"),
				KeyType:       "HASH",
			},
		},
		TableName: aws.String(db.buildTable),
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(1),
			WriteCapacityUnits: aws.Int64(1),
		},
	}
	return utils.CreateTable(context.Background(), db.client, &createTableInput)
}

func (db DynamoDb) InitializeJobs() error {
	ttlCursor := time.Now().AddDate(0, 0, -manager.DefaultTtlDays)
	// Load all jobs in an advanced stage of processing (completed, failed, delayed, waiting, started, skipped), so that
	// we know which jobs have already been dequeued.
	if err := db.loadJobs(job.JobStage_Completed, ttlCursor); err != nil {
		return err
	} else if err = db.loadJobs(job.JobStage_Failed, ttlCursor); err != nil {
		return err
	} else if err = db.loadJobs(job.JobStage_Canceled, ttlCursor); err != nil {
		return err
	} else if err = db.loadJobs(job.JobStage_Waiting, ttlCursor); err != nil {
		return err
	} else if err = db.loadJobs(job.JobStage_Started, ttlCursor); err != nil {
		return err
	} else if err = db.loadJobs(job.JobStage_Dequeued, ttlCursor); err != nil {
		return err
	} else {
		return db.loadJobs(job.JobStage_Skipped, ttlCursor)
	}
}

func (db DynamoDb) loadJobs(stage job.JobStage, cursor time.Time) error {
	return db.iterateByStage(stage, cursor, true, func(jobState job.JobState) bool {
		// Write loaded jobs to the cache
		db.cache.WriteJob(jobState)
		// Return true so that we keep on iterating
		return true
	})
}

func (db DynamoDb) QueueJob(jobState job.JobState) error {
	// Only write this job to the database since that's where our de/queueing is expected to happen from. The cache is
	// just a hash-map from job IDs to job state for ACTIVE jobs (jobs are not added to the cache until they are in
	// progress). This also means that we don't need to write jobs to the database if they're already in the cache.
	if _, found := db.cache.JobById(jobState.Job); !found {
		return db.WriteJob(jobState)
	}
	return nil
}

// QueuedJobs returns jobs in order of their DB timestamps that have not yet been picked up from the database and are
// thus not in the cache. We use the fact that a new job is not in the cache yet to determine whether a job is truly new
// or if it has already started being processed.
func (db DynamoDb) QueuedJobs() []job.JobState {
	// If available, use the timestamp of the previously found first job not already in processing as the start of the
	// current database search. We can't know for sure that all subsequent jobs are unprocessed (e.g. force deploys or
	// anchors could mess up that assumption), but what we can say for sure is that all prior jobs have at least entered
	// processing, and so we haven't missed any jobs. Otherwise, look for jobs queued at most 1 day in the past.
	var cursor time.Time
	ttlCursor := time.Now().AddDate(0, 0, -manager.DefaultTtlDays)
	if db.cursor.After(ttlCursor) {
		cursor = db.cursor
	} else {
		cursor = ttlCursor
	}
	jobs := make([]job.JobState, 0, 0)
	cursorSet := false
	if err := db.iterateByStage(job.JobStage_Queued, cursor, true, func(jobState job.JobState) bool {
		// If a job is not already in the cache, append it since it hasn't been dequeued yet.
		if _, found := db.cache.JobById(jobState.Job); !found {
			jobs = append(jobs, jobState)
			// Set the cursor to the timestamp of the first job that is not already in processing (see `iterateByStage`
			// for explanation why).
			if !cursorSet {
				db.cursor = jobState.Ts
				cursorSet = true
			}
		}
		// Return true so that we keep on iterating.
		return true
	}); err != nil {
		log.Printf("queuedJobs: failed iteration through jobs: %v", err)
	}
	// If the cursor is still unset, then we found no jobs that weren't already in processing or done. In that case, set
	// the cursor to "now" so we know to search from this point in time onwards. There's no point looking up jobs from
	// the past that we know no longer need any processing.
	if !cursorSet {
		db.cursor = time.Now()
	}
	return jobs
}

// OrderedJobs returns jobs in order of their DB timestamps that are in the cache in a certain stage of processing
func (db DynamoDb) OrderedJobs(jobStage job.JobStage) []job.JobState {
	jobs := make([]job.JobState, 0, 0)
	if err := db.iterateByStage(jobStage, db.cursor, true, func(jobState job.JobState) bool {
		if cachedJob, found := db.cache.JobById(jobState.Job); found && cachedJob.Stage == jobStage {
			jobs = append(jobs, jobState)
		}
		// Return true so that we keep on iterating.
		return true
	}); err != nil {
		log.Printf("orderedJobs: failed iteration through jobs: %v", err)
	}
	return jobs
}

func (db DynamoDb) IterateByType(jobType job.JobType, asc bool, iter func(job.JobState) bool) error {
	return db.iterateByType(jobType, time.Now().AddDate(0, 0, -manager.DefaultTtlDays), asc, iter)
}

func (db DynamoDb) iterateByStage(jobStage job.JobStage, cursor time.Time, asc bool, iter func(job.JobState) bool) error {
	// Only look for jobs up till the current time. This allows us to schedule jobs in the future (e.g. smoke tests to
	// start a few minutes after a deployment is complete).
	return db.iterateEvents(&dynamodb.QueryInput{
		TableName:              aws.String(db.jobTable),
		IndexName:              aws.String(job.StageTsIndex),
		KeyConditionExpression: aws.String("#stage = :stage and #ts between :ts and :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":stage": &types.AttributeValueMemberS{Value: string(jobStage)},
			":ts":    &types.AttributeValueMemberN{Value: strconv.FormatInt(cursor.UnixNano(), 10)},
			":now":   &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().UnixNano(), 10)},
		},
		ExpressionAttributeNames: map[string]string{
			"#stage": "stage",
			"#ts":    "ts",
		},
		ScanIndexForward: aws.Bool(asc),
	}, iter)
}

func (db DynamoDb) iterateByType(jobType job.JobType, cursor time.Time, asc bool, iter func(job.JobState) bool) error {
	// Only look for jobs up till the current time. This allows us to schedule jobs in the future (e.g. smoke tests to
	// start a few minutes after a deployment is complete).
	return db.iterateEvents(&dynamodb.QueryInput{
		TableName:              aws.String(db.jobTable),
		IndexName:              aws.String(job.TypeTsIndex),
		KeyConditionExpression: aws.String("#type = :type and #ts between :ts and :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":type": &types.AttributeValueMemberS{Value: string(jobType)},
			":ts":   &types.AttributeValueMemberN{Value: strconv.FormatInt(cursor.UnixNano(), 10)},
			":now":  &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().UnixNano(), 10)},
		},
		ExpressionAttributeNames: map[string]string{
			"#type": "type",
			"#ts":   "ts",
		},
		ScanIndexForward: aws.Bool(asc),
	}, iter)
}

func (db DynamoDb) iterateEvents(queryInput *dynamodb.QueryInput, iter func(job.JobState) bool) error {
	p := dynamodb.NewQueryPaginator(db.client, queryInput)
	for p.HasMorePages() {
		err := func() error {
			ctx, cancel := context.WithTimeout(context.Background(), manager.DefaultHttpWaitTime)
			defer cancel()

			page, err := p.NextPage(ctx)
			if err != nil {
				return err
			}
			var jobsPage []job.JobState
			err = attributevalue.UnmarshalListOfMapsWithOptions(page.Items, &jobsPage, func(options *attributevalue.DecoderOptions) {
				options.DecodeTime = attributevalue.DecodeTimeAttributes{
					S: utils.TsDecode,
					N: utils.TsDecode,
				}
			})
			if err != nil {
				log.Printf("initialize: unable to unmarshal jobState: %v", err)
				return err
			}
			for _, jobState := range jobsPage {
				if jobState.Type == job.JobType_Deploy {
					// Marshal layout back into `Layout` structure
					if layout, found := jobState.Params[job.JobParam_Layout].(map[string]interface{}); found {
						var marshaledLayout manager.Layout
						if err = mapstructure.Decode(layout, &marshaledLayout); err != nil {
							return err
						}
						jobState.Params[job.JobParam_Layout] = marshaledLayout
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

func (db DynamoDb) AdvanceJob(jobState job.JobState) error {
	if err := db.WriteJob(jobState); err != nil {
		return err
	}
	db.cache.WriteJob(jobState)
	return nil
}

func (db DynamoDb) WriteJob(jobState job.JobState) error {
	// Generate a new UUID for every job update
	jobState.Id = uuid.New().String()
	// Set entry expiration
	jobState.Ttl = time.Now().Add(manager.DefaultJobStateTtl)
	if attributeValues, err := attributevalue.MarshalMapWithOptions(jobState, func(options *attributevalue.EncoderOptions) {
		options.EncodeTime = func(time time.Time) (types.AttributeValue, error) {
			return &types.AttributeValueMemberN{Value: strconv.FormatInt(time.UnixNano(), 10)}, nil
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
