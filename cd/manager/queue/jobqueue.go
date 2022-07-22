package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/3box/pipeline-tools/cd/manager"
)

type JobQueue struct {
	Block      chan bool
	deployment manager.Deployment
	queue      manager.Queue
	db         manager.Database
	apiGw      manager.ApiGw
}

func NewJobQueue(q manager.Queue, db manager.Database, d manager.Deployment, a manager.ApiGw) (*JobQueue, error) {
	block := make(chan bool)
	return &JobQueue{block, d, q, db, a}, nil
}

func (jq *JobQueue) Stop() error {
	// TODO: Clean up client connections and safely stop processing
	<-jq.Block
	return nil
}

func (jq *JobQueue) ProcessQueue(
	exit chan bool,
) {
	messages := make(chan manager.QueueMessage)
	jobs := make(chan manager.Job)

	receiveMessagesBlock := make(chan bool)
	processMessagesBlock := make(chan bool)
	processJobsBlock := make(chan bool)

	go jq.receiveMessages(exit, receiveMessagesBlock, messages)
	go jq.processMessages(receiveMessagesBlock, processMessagesBlock, messages, jobs)
	go jq.processJobs(processMessagesBlock, processJobsBlock, jobs)

	<-receiveMessagesBlock
	<-processMessagesBlock
	<-processJobsBlock
	close(jq.Block)
}

func (jq *JobQueue) receiveMessages(
	exit chan bool,
	block chan bool,
	messages chan manager.QueueMessage,
) {
	for {
		select {
		case <-exit:
			log.Println("Stop receiving messages...")
			close(messages)
			close(block)
			return
		default:
			jq.queue.Receive(messages, context.Background())
		}
	}
}

func (jq *JobQueue) processMessages(
	exit chan bool,
	block chan bool,
	messages chan manager.QueueMessage,
	jobs chan manager.Job,
) {
	for {
		select {
		case <-exit:
			log.Println("Stop processing messages...")
			close(jobs)
			close(block)
			return
		default:
			for message := range messages {
				job := manager.Job{
					Id:   message.Id,
					RxId: message.ReceiptId,
				}
				messageGroupId := message.Attributes["MessageGroupId"]
				switch messageGroupId {
				case "anchor":
					job.Type = manager.JobType_Anchor
				case "deploy":
					job.Type = manager.JobType_Deploy
					if err := json.Unmarshal([]byte(message.Body), &job.Params); err != nil {
						// TODO: How to handle error?
						log.Print("sqs: could not unmarshal job: %w", err)
					}
				case "test_e2e":
					job.Type = manager.JobType_TestE2E
				case "test_smoke":
					job.Type = manager.JobType_TestSmoke
				default:
					log.Printf("no job type for message with attribute (MessageGroupId=%v)", messageGroupId)
				}
				if job.Type > 0 {
					jobs <- job
				}
			}
		}
	}
}

func (jq *JobQueue) processJobs(
	exit chan bool,
	block chan bool,
	jobs chan manager.Job,
) {
	for {
		select {
		case <-exit:
			log.Println("Stop processing jobs...")
			close(block)
			return
		default:
			for job := range jobs {
				switch job.Type {
				case manager.JobType_Anchor:
					fmt.Printf("anchor: %v", job)
					if err := jq.deployment.Launch("ceramic-dev-cas", "ceramic-dev-cas-anchor", "ceramic-dev-cas-anchor"); err != nil {
						log.Printf("job: deploy error: %s", err)
					}
				case manager.JobType_Deploy:
					log.Printf("deploy: %v", job)
				case manager.JobType_TestE2E:
					log.Printf("e2e: %v", job)
				case manager.JobType_TestSmoke:
					log.Printf("smoke: %v", job)
					// TODO: Replace this API call with an ECS task launch.
					resourceId := os.Getenv("SMOKE_TEST_RESOURCE_ID")
					restApiId := os.Getenv("SMOKE_TEST_REST_API_ID")
					if resp, err := jq.apiGw.Invoke("GET", resourceId, restApiId, ""); err != nil {
						log.Printf("smoke: api call failed: %s", err)
					} else {
						log.Printf("result: %s", resp)
					}
				default:
					// TODO: Handle error
					fmt.Errorf("sqs: invalid job: %v", job)
				}
			}
		}
	}
}
