package ci

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"

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
		_sock:		   client.network."unix:///var/run/docker.sock".connect
		_registryName: "local-registry"
		_registryPort: "5042"
		_registryRef: "localhost:\(_registryPort)"

		version: utils.#Version & {
			src: _source
		}

		test: utils.#TestNode & {
			src: _source
		}

		image: {
			_clean: cli.#Run & {
				env: {
					REGISTRY_NAME: _registryName
					REGISTRY_REF:  _registryRef
				}
				host:   _sock
				always: true
				command: {
					name: "sh"
					flags: "-c": #"""
						docker rm -f "$REGISTRY_NAME"
						docker rmi -f cas "$REGISTRY_REF/cas" "$REGISTRY_REF/runner"
					"""#
				}
			}
			_images: cli.#Run & {
				env: {
					REGISTRY_NAME: _registryName
					REGISTRY_PORT: _registryPort
					REGISTRY_REF:  _registryRef
					DEP:		   "\(_clean.success)"
				}
				host:   _sock
				always: true
        		workdir: "./src"
				mounts: source: {
					dest:     "/src"
					contents: _source
				}
				command: {
					name: "sh"
					flags: "-c": #"""
						docker run --rm -d -p "$REGISTRY_PORT:5000" --name "$REGISTRY_NAME" registry
						docker build -f Dockerfile -t cas -t "$REGISTRY_REF/cas" .
						docker build -f Dockerfile.runner -t "$REGISTRY_REF/runner" .
						docker push -a "$REGISTRY_REF/cas"
						docker push -a "$REGISTRY_REF/runner"
					"""#
				}
			}
			cas: {
				env: DEP: "\(_images.success)"
				docker.#Pull & {
					source: "\(_registryRef)/cas"
				}
			}
			runner: {
				env: DEP: "\(_images.success)"
				docker.#Pull & {
					source: "\(_registryRef)/runner"
				}
			}
		}

		verify: {
			cas: utils.#TestImage & {
				testImage:     image.cas.output
				testImageName: "cas-ci-test"
				endpoint:	   "api/v0/healthcheck"
				port:		   8081
				dockerHost:    _sock
			}
			runner: utils.#TestImage & {
				testImage:     image.runner.output
				testImageName: "runner-ci-test"
				endpoint:	   "api/v0/healthcheck"
				port:		   8081
				dockerHost:    _sock
			}
		}

		push: [Region=aws.#Region]: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: {
			_params: {
				repo:     _repo
				envTag:   EnvTag
				branch:   Branch
				sha:      Sha
				shaTag:   ShaTag
			}
			ecr: utils.#ECR & {
				repo: "ceramic-\(EnvTag)-cas"
				env: {
					AWS_ACCOUNT_ID: 	client.env.AWS_ACCOUNT_ID
					AWS_ECR_SECRET: 	client.commands.aws.stdout
					AWS_REGION: 		Region
				}
				params: _params & {
					image: actions.image.runner.output
				}
			}
			dockerhub: utils.#Dockerhub & {
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN: 	client.env.DOCKERHUB_TOKEN
				}
				params: _params & {
					image: actions.image.cas.output
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
