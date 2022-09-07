# Ceramic Pipeline Tools

This repository is a collection of Go code and [Dagger](https://www.dagger.io) plans for building, testing, and deploying various Ceramic network components.

## Organization

### CI

The CI portion of the code consists of Dagger plans for:
- Building code
- Running unit tests
- Building Docker images
- Validating Docker images
- Pushing Docker images to Dockerhub and ECR
- Creating CD jobs (deployments, e2e tests, smoke tests)

### CD

The CD portion of the code consists of the CD Manager (written in Go), which is responsible for:
- Reading jobs from a job queue (implemented over DynamoDB used as a time-series database)
- Deploying services or standalone tasks to one or more ECS clusters
- Running smoke tests
- Running integrations tests
- Launching anchor workers
- Coordinating between:
  - Compatible jobs that can run simultaneously, e.g. smoke tests and e2e tests
  - Incompatible jobs that can't run simultaneously, e.g. deployments and all other jobs
