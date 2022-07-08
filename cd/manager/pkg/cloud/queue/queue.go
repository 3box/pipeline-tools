package queue

import (
	"context"
	"log"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud"
)

type JobQueue struct {
	deployment cloud.Deployment
	queue      cloud.Queue
	db         cloud.Database
}

func NewJobQueue(d cloud.Deployment, q cloud.Queue, db cloud.Database) (*JobQueue, error) {
	return &JobQueue{d, q, db}, nil
}

func (jq *JobQueue) ProcessQueue() {
	jobChan := make(chan cloud.Job, 1)
	go jq.pollQueue(jobChan)

	for job := range jobChan {
		// Store job in DB

		// Process job
		switch job.Type {
		case cloud.JobType_Deploy:
			log.Printf("deploy: %v", job)
		case cloud.JobType_Anchor:
			log.Printf("anchor: %v", job)
			if err := jq.deployment.LaunchService("ceramic-dev-node", "ceramic-dev"); err != nil {
				log.Printf("job: deploy error: %s", err)
			}
		case cloud.JobType_E2E:
			log.Printf("e2e: %v", job)
		case cloud.JobType_Smoke:
			log.Printf("smoke: %v", job)
		default:
			log.Printf("invalid job: %v", job)
		}
	}
}

func (jq *JobQueue) pollQueue(ch chan cloud.Job) {
	for {
		log.Println("Polling for jobs...")
		jobs, err := jq.queue.Receive(context.Background())
		if err != nil {
			log.Printf("error polling queue: %s", err)
		}
		for _, job := range jobs {
			ch <- job
		}
	}
}
