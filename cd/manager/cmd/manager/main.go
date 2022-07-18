package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud/aws"
	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud/queue"
	"github.com/3box/pipeline-tools/cd/manager/pkg/server"
	"github.com/alecthomas/kong"
)

type CliOptions struct {
	Port string `short:"p" help:"Port for status server"`
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	var cli CliOptions
	kong.Parse(&cli)

	waitGroup := new(sync.WaitGroup)
	waitGroup.Add(3)
	shutdownBlock := make(chan bool)

	serverAddress := os.Getenv("CD_MANAGER_SERVER_ADDR")
	serverContext := context.Background()
	serverInstance := server.SetUp(serverAddress, serverContext)
	if len(serverAddress) == 0 {
		serverAddress = ":80"
	}
	go func() {
		defer waitGroup.Done()
		log.Printf("\nServer is listening at %s", serverAddress)
		serverInstance.ListenAndServe()
		log.Println("Server stopped listening")
	}()

	cfg, accountId, err := aws.Config()
	if err != nil {
		log.Fatalf("Failed to create AWS cfg: %q", err)
	}
	env := os.Getenv("ENV")
	q := aws.NewSqs(cfg, accountId, "dev")
	db := aws.NewDynamoDb(cfg, env)
	d := aws.NewEcs(cfg, env)
	jq, err := queue.NewJobQueue(q, db, d)
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
