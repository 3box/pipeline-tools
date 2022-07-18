package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud"
)

type JobQueue struct {
	Block      chan bool
	deployment cloud.Deployment
	queue      cloud.Queue
	db         cloud.Database
}

func NewJobQueue(q cloud.Queue, db cloud.Database, d cloud.Deployment) (*JobQueue, error) {
	block := make(chan bool)
	return &JobQueue{block, d, q, db}, nil
}

func (jq *JobQueue) Stop() error {
	// TODO: Clean up client connections and safely stop processing
	<-jq.Block
	return nil
}

func (jq *JobQueue) ProcessQueue(
	exit chan bool,
) {
	messages := make(chan cloud.QueueMessage)
	jobs := make(chan cloud.Job)

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
	messages chan cloud.QueueMessage,
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
	messages chan cloud.QueueMessage,
	jobs chan cloud.Job,
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
				job := cloud.Job{
					Id:   message.Id,
					RxId: message.ReceiptId,
				}
				messageGroupId := message.Attributes["MessageGroupId"]
				switch messageGroupId {
				case "anchor":
					job.Type = cloud.JobType_Anchor
				case "deploy":
					job.Type = cloud.JobType_Deploy
					if err := json.Unmarshal([]byte(message.Body), &job.Params); err != nil {
						// TODO: How to handle error?
						log.Print("sqs: could not unmarshal job: %w", err)
					}
				case "test_e2e":
					job.Type = cloud.JobType_TestE2E
				case "test_smoke":
					job.Type = cloud.JobType_TestSmoke
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
	jobs chan cloud.Job,
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
				case cloud.JobType_Anchor:
					fmt.Printf("anchor: %v", job)
					if err := jq.deployment.Launch("ceramic-dev-cas", "ceramic-dev-cas-anchor", "ceramic-dev-cas-anchor"); err != nil {
						log.Printf("job: deploy error: %s", err)
					}
				case cloud.JobType_Deploy:
					log.Printf("deploy: %v", job)
				case cloud.JobType_TestE2E:
					log.Printf("e2e: %v", job)
				case cloud.JobType_TestSmoke:
					log.Printf("smoke: %v", job)
				default:
					// TODO: Handle error
					fmt.Errorf("sqs: invalid job: %v", job)
				}
			}
		}
	}
}
