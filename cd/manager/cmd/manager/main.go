package main

import (
	"log"
	"os"

	"github.com/alecthomas/kong"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud/aws"
	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud/queue"
)

type CliOptions struct {
	Port string `short:"p" help:"Port for status server"`
}

func main() {
	var cli CliOptions
	kong.Parse(&cli)

	cfg, accountId, err := aws.Config()
	if err != nil {
		log.Fatalf("Failed to create AWS cfg: %q", err)
	}

	env := os.Getenv("ENV")

	// Launch server to report status

	// Create services for job queue
	q := aws.NewSqs(cfg, accountId, env)
	db := aws.NewDynamoDb(cfg, env)
	d := aws.NewEcs(cfg, env)

	// Launch worker to process queue
	jq, err := queue.NewJobQueue(d, q, db)
	if err != nil {
		log.Fatalf("Failed to create job queue: %q", err)
	}
	jq.ProcessQueue()
}
