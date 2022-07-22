package main

import (
	"context"
	"fmt"
	"github.com/3box/pipeline-tools/cd/manager"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"

	"github.com/3box/pipeline-tools/cd/manager/aws"
	"github.com/3box/pipeline-tools/cd/manager/queue"
	"github.com/3box/pipeline-tools/cd/manager/server"
)

type CliOptions struct {
	Port string `short:"p" help:"Port for status server"`
}

func main() {
	if err := godotenv.Load("env/.env"); err != nil {
		log.Fatal("Error loading .env file")
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	var cli CliOptions
	kong.Parse(&cli)

	waitGroup := new(sync.WaitGroup)
	shutdownChan := make(chan bool)

	// Create and initialize the job queue before the API handling
	jq := createJobQueue(waitGroup, shutdownChan)

	// Setup API handling
	serverInstance := startServer(waitGroup, jq)

	// Shutdown processing
	waitGroup.Add(1)
	go shutdown(waitGroup, func() bool {
		close(shutdownChan)
		if err := serverInstance.Shutdown(context.Background()); err != nil {
			fmt.Printf("Server error on shutdown: %+v", err)
		}
		return true
	})
	waitGroup.Wait()
}

func startServer(waitGroup *sync.WaitGroup, jq manager.Queue) *http.Server {
	serverAddress := os.Getenv("SERVER_ADDR")
	if len(serverAddress) == 0 {
		serverAddress = "0.0.0.0"
	}
	serverPort := os.Getenv("SERVER_PORT")
	if len(serverPort) == 0 {
		serverPort = "8080"
	}
	serverInstance := server.Setup(serverAddress+":"+serverPort, jq)
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		log.Printf("\nServer is listening at %s:%s", serverAddress, serverPort)
		serverInstance.ListenAndServe()
		log.Println("Server stopped listening")
	}()
	return &serverInstance
}

func createJobQueue(waitGroup *sync.WaitGroup, shutdownCh chan bool) manager.Queue {
	cfg, err := aws.Config()
	if err != nil {
		log.Fatalf("Failed to create AWS cfg: %q", err)
	}
	db := aws.NewDynamoDb(cfg)
	if err = db.InitializeJobs(); err != nil {
		log.Fatalf("Failed to populate jobs from database: %q", err)
	}
	deployment := aws.NewEcs(cfg)
	api := aws.NewApi(cfg)
	jq, err := queue.NewJobQueue(db, deployment, api, shutdownCh)
	if err != nil {
		log.Fatalf("Failed to create job queue: %q", err)
	}
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		log.Println("Started job queue processing")
		jq.ProcessJobs()
		log.Println("Stopped job queue processing")
	}()
	return jq
}

func shutdown(waitGroup *sync.WaitGroup, cleanup func() bool) {
	interruptCh := make(chan os.Signal, 1)
	signal.Notify(interruptCh, os.Interrupt)
	<-interruptCh
	fmt.Println("\nShutting down gracefully... (Enter ctrl+c to force shut down)")
	if cleanup() {
		waitGroup.Done()
		fmt.Println("Done")
		return
	}
	signal.Notify(interruptCh, syscall.SIGTERM)
	<-interruptCh
	waitGroup.Done()
}
