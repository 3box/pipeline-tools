package ci

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

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
		]
	}
	// Dockerfile
	client: filesystem: dockerfile: read: {
		path: "."
		contents: dagger.#FS
		include: ["Dockerfile"]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_repo:			"go-ipfs-daemon"
		_source:		client.filesystem.source.read.contents
		_dockerfile:	client.filesystem.dockerfile.read.contents

		version: utils.#Version & {
			src: _source
		}

		test: utils.#Version & {
			src: _source
		}

		image: docker.#Dockerfile & {
			_file: core.#ReadFile & {
				input: _dockerfile
				path:  "Dockerfile"
			}
			source: _source
			dockerfile: contents: _file.contents
		}

		verify: image.#Test & {
			testImage:  image.output
			dockerHost: client.network."unix:///var/run/docker.sock".connect
		}

		push: [Region=aws.#Region]: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: utils.#Push & {
			ecrRepo: "go-ipfs-\(EnvTag)"
			env: {
				AWS_ACCOUNT_ID: 	client.env.AWS_ACCOUNT_ID
				AWS_ECR_SECRET: 	client.commands.aws.stdout
				AWS_REGION: 		Region
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
