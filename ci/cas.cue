package ci

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/aws"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"

	"github.com/3box/pipeline-tools/utils"
)

dagger.#Plan & {
	client: env: {
		// Secrets
		AWS_ACCOUNT_ID:        string
		AWS_REGION:    		   string
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		DOCKERHUB_USERNAME:    string
		DOCKERHUB_TOKEN:       dagger.#Secret
		// Runtime
		DAGGER_LOG_FORMAT:     string | *"plain"
		DAGGER_LOG_LEVEL:      string | *"info"
	}
	client: commands: aws: {
		name: "aws"
		args: ["ecr", "get-login-password"]
		stdout: dagger.#Secret
	}
	// Full source to use for building/testing code
	client: filesystem: source: read: {
		path: "."
		contents: dagger.#FS
		exclude: [
			".github",
			"cue.mod",
			"node_modules",
			"build",
		]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_repo:	   	   "ceramic-anchor-service"
		_source:   	   client.filesystem.source.read.contents
		_dockerHost:   client.network."unix:///var/run/docker.sock".connect

		version: utils.#Version & {
			src: _source
		}

		test: utils.#TestNode & {
			src: _source
		}

		image: docker.#Dockerfile & {
			source: _source
		}

		verify: {
			_endpoint:	"api/v0/healthcheck"
			_port:		8081
			_cli: alpine.#Build & {
				packages: {
					bash:             {}
					curl:             {}
					"docker-cli":     {}
					"docker-compose": {}
				}
			}
			healthcheck: bash.#Run & {
				env: {
					URL:     "http://0.0.0.0:\(_port)/\(_endpoint)"
					TIMEOUT: "60"
				}
				input: _cli.output
				workdir: "/src"
				mounts: source: {
					dest:     "/src"
					contents: _source
				}
				mounts: docker: {
					contents: _dockerHost
					dest:     "/var/run/docker.sock"
				}
				always: true
                script: contents: #"""
                    docker-compose down
                    docker-compose up -d
                    timeout=$TIMEOUT
                    until [[ $timeout -le 0 ]]; do
                        echo Waiting for image to start...
                        curl --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

                        if grep -q "200 OK" curl.out
                        then
                            echo Healthcheck passed
                            docker-compose down
                            exit 0
                        fi

                        sleep 1
                        timeout=$(( timeout - 1 ))
                    done

                    if [ $timeout -le 0 ]; then
                        echo Healthcheck failed
                        docker-compose down
                        exit 1
                    fi
                """#
			}
		}

		push: [Region=aws.#Region]: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: {
			dockerhub: utils.#Dockerhub & {
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN: 	client.env.DOCKERHUB_TOKEN
				}
				params: {
					repo:     _repo
					envTag:   EnvTag
					branch:   Branch
					sha:      Sha
					shaTag:   ShaTag
					image:    actions.image.output
				}
			}
		}

		queue: [Region=aws.#Region]: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: utils.#Queue & {
			env: {
				AWS_ACCOUNT_ID: 	   client.env.AWS_ACCOUNT_ID
				AWS_ACCESS_KEY_ID: 	   client.env.AWS_ACCESS_KEY_ID
				AWS_SECRET_ACCESS_KEY: client.env.AWS_SECRET_ACCESS_KEY
				AWS_REGION: 		   Region
			}
			params: {
				event:	  "deploy"
				repo:     _repo
				envTag:   EnvTag
				branch:   Branch
				sha:      Sha
				shaTag:   ShaTag
			}
		}
	}
}
