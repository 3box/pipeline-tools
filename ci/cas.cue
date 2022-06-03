package ci

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
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

		version: utils.#Version & {
			src: _source
		}

		test: utils.#TestNode & {
			src: _source
		}

		image: docker.#Dockerfile & {
			source: _source
		}

		verify: utils.#TestImage & {
			testImage:     image.output
			endpoint:	   "api/v0/healthcheck"
			port:		   8081
			dockerHost:    client.network."unix:///var/run/docker.sock".connect
		}

		push: [Region=aws.#Region]: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: {
			_params: {
				repo:     _repo
				envTag:   EnvTag
				branch:   Branch
				sha:      Sha
				shaTag:   ShaTag
				image:    actions.image.output
			}
			ecr: utils.#ECR & {
				repo: "ceramic-\(EnvTag)-cas"
				env: {
					AWS_ACCOUNT_ID: 	client.env.AWS_ACCOUNT_ID
					AWS_ECR_SECRET: 	client.commands.aws.stdout
					AWS_REGION: 		Region
				}
				params: _params
			}
			dockerhub: utils.#Dockerhub & {
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN: 	client.env.DOCKERHUB_TOKEN
				}
				params: _params
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
