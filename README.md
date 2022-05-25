# Ceramic Pipeline Tools

This repository is a collection of [Dagger](https://www.dagger.io) plans for building, testing, and deploying various Ceramic network components.

## Organization

Dagger scripts are organized in 3 sub-directories: `ci`, `cd`, and `utils`.

### CI

The `ci` directory contains the Dagger scripts for:
- Building code
- Running unit tests
- Building Docker images
- Validating Docker images
- Pushing Docker images to Dockerhub and ECR
- Posting to a job queue

### CD

The `cd` directory contains the Dagger scripts for:
- Reading from a job queue
- Deploying a service to one or more clusters in an environment
- Running smoke tests
- Running integrations tests
- Launching an anchor worker

### Utilities

The `utils` directory contains the Dagger scripts for various pieces of CI/CD processing that can be shared between tasks:
- Post event to job queue
