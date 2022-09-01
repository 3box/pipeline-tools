# CD Manager

Schedules deployments, tests, and anchors

## Usage

Install golang 1.18
```sh
go install golang.org/dl/go1.18@latest
go1.18 download
go1.18 env GOROOT # see where it is located
```

Setup manager environment
```sh
cd cd/manager/cmd/manager
mkdir env
touch env/.env
```

Build and run the manager
```sh
# cd cd/manager/cmd/manager
go build . # or go1.18 build .
./manager
```
