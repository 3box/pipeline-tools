package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/aws"
	"github.com/3box/pipeline-tools/cd/manager/jobmanager"
	"github.com/3box/pipeline-tools/cd/manager/notifs"
	"github.com/3box/pipeline-tools/cd/manager/repository"
	"github.com/3box/pipeline-tools/cd/manager/server"
)

// TODO: Add more comments across the code

func main() {
	if err := godotenv.Load("env/.env"); err != nil {
		log.Fatal("Error loading .env file")
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	waitGroup := new(sync.WaitGroup)
	shutdownChan := make(chan bool)

	// Create and initialize the job queue before the API handling
	m := createJobQueue(waitGroup, shutdownChan)

	// Setup API handling
	serverInstance := startServer(waitGroup, m)

	// Shutdown processing
	waitGroup.Add(1)
	go shutdown(waitGroup, func() bool {
		close(shutdownChan)
		if err := serverInstance.Shutdown(context.Background()); err != nil {
			fmt.Printf("Server error on shutdown: %v", err)
		}
		return true
	})
	waitGroup.Wait()
}

func startServer(waitGroup *sync.WaitGroup, m manager.Manager) *http.Server {
	serverAddress := os.Getenv("SERVER_ADDR")
	if len(serverAddress) == 0 {
		serverAddress = "0.0.0.0"
	}
	serverPort := os.Getenv("SERVER_PORT")
	if len(serverPort) == 0 {
		serverPort = "8080"
	}
	serverInstance := server.Setup(serverAddress+":"+serverPort, m)
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		log.Printf("\nServer is listening at %s:%s", serverAddress, serverPort)
		serverInstance.ListenAndServe()
		log.Println("Server stopped listening")
	}()
	return &serverInstance
}

func createJobQueue(waitGroup *sync.WaitGroup, shutdownCh chan bool) manager.Manager {
	cfg, err := aws.Config()
	if err != nil {
		log.Fatalf("Failed to create AWS cfg: %q", err)
	}
	cache := jobmanager.NewJobCache()
	db := aws.NewDynamoDb(cfg, cache)
	if err = db.InitializeJobs(); err != nil {
		log.Fatalf("failed to populate jobs from database: %q", err)
	}
	deployment := aws.NewEcs(cfg)
	apiGw := aws.NewApiGw(cfg)
	repo := repository.NewRepository()
	notifs, err := notifs.NewJobNotifs(db)
	if err != nil {
		log.Fatalf("failed to initialize notifications: %q", err)
	}
	jobManager, err := jobmanager.NewJobManager(cache, db, deployment, apiGw, repo, notifs)
	if err != nil {
		log.Fatalf("failed to create job queue: %q", err)
	}
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		log.Println("started job queue processing")
		jobManager.ProcessJobs(shutdownCh)
		log.Println("stopped job queue processing")
	}()
	return jobManager
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
