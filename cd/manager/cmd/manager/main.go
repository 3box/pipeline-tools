package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud/aws"
	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud/queue"
	"github.com/3box/pipeline-tools/cd/manager/pkg/server"
)

type CliOptions struct {
	Port string `short:"p" help:"Port for status server"`
}

func main() {
	envFile := "env/.env"
	env := os.Getenv("ENV_TAG")
	if len(env) > 0 {
		envFile += "." + env
	}
	err := godotenv.Load(envFile)
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	var cli CliOptions
	kong.Parse(&cli)

	waitGroup := new(sync.WaitGroup)
	waitGroup.Add(3)
	shutdownBlock := make(chan bool)

	serverAddress := os.Getenv("SERVER_ADDR")
	if len(serverAddress) == 0 {
		serverAddress = "0.0.0.0"
	}
	serverPort := os.Getenv("SERVER_PORT")
	if len(serverAddress) == 0 {
		serverAddress = "8080"
	}
	serverContext := context.Background()
	serverInstance := server.SetUp(serverAddress+":"+serverPort, serverContext)
	go func() {
		defer waitGroup.Done()
		log.Printf("\nServer is listening at %s", serverAddress)
		serverInstance.ListenAndServe()
		log.Println("Server stopped listening")
	}()

	// TODO: We either need to skip the following code, or use a fake SQS queue when validating the Docker image
	cfg, accountId, err := aws.Config()
	if err != nil {
		log.Fatalf("Failed to create AWS cfg: %q", err)
	}
	q := aws.NewSqs(cfg, accountId, env)
	db := aws.NewDynamoDb(cfg, env)
	d := aws.NewEcs(cfg, env)
	a := aws.NewApi(cfg)
	jq, err := queue.NewJobQueue(q, db, d, a)
	if err != nil {
		log.Fatalf("Failed to create job queue: %q", err)
	}
	go func() {
		defer waitGroup.Done()
		log.Println("Started job queue processing")
		jq.ProcessQueue(shutdownBlock)
		log.Println("Stopped job queue processing")
	}()

	go shutDown(waitGroup, func() bool {
		close(shutdownBlock)
		jq.Stop()
		if err := serverInstance.Shutdown(context.Background()); err != nil {
			fmt.Printf("Server error on shutdown: %+v", err)
		}
		return true
	})

	waitGroup.Wait()
}

func shutDown(waitGroup *sync.WaitGroup, cleanUp func() bool) {
	signalInterruptChannel := make(chan os.Signal, 1)
	signal.Notify(signalInterruptChannel, os.Interrupt)
	<-signalInterruptChannel
	fmt.Println("\nShutting down gracefully... (Enter ctrl+c to force shut down)")
	if cleanUp() {
		waitGroup.Done()
		fmt.Println("Done")
		return
	}
	signal.Notify(signalInterruptChannel, syscall.SIGTERM)
	<-signalInterruptChannel
	waitGroup.Done()
}
