package ceramic

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"

	"github.com/3box/pipelinetools/utils"
	"github.com/3box/pipelinetools/utils/testing"
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
		DAGGER_LOG_FORMAT:     string | *"auto"
		ACTIONS_RUNTIME_TOKEN: string | *""
		ACTIONS_CACHE_URL:     string | *""
	}
	client: commands: aws: {
		name: "aws"
		args: ["ecr", "get-login-password"]
		stdout: dagger.#Secret
	}
	// Full source to use for building/testing code
	client: filesystem: fullSource: read: {
		path: "."
		contents: dagger.#FS
		exclude: [
			".github",
			"cue.mod",
		]
	}
	// Subset of source required to build Docker image
	client: filesystem: imageSource: read: {
		path: "."
		contents: dagger.#FS
		include: [
			"package.json",
			"package-lock.json",
			"lerna.json",
			"tsconfig.json",
			"packages",
			"types"
		]
	}
	// Dockerfile
	client: filesystem: dockerfile: read: {
		path: "."
		contents: dagger.#FS
		include: ["Dockerfile.daemon"]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_testImageName: "js-ceramic-ci"
		_fullSource:	client.filesystem.fullSource.read.contents
		_imageSource:	client.filesystem.imageSource.read.contents
		_dockerfile:	client.filesystem.dockerfile.read.contents

		version: utils.#Version & {
			src: _fullSource
		}

		test: {
			// These steps can be run in parallel in separate containers
			testJs:  testing.#Node & {
				src: _fullSource
				run: env: IPFS_FLAVOR: "js"
				run: env: NODE_OPTIONS: "--max_old_space_size=4096"
			}
			testGo:  testing.#Node & {
				src: _fullSource
				run: env: IPFS_FLAVOR: "go"
			}
		}

		image: docker.#Dockerfile & {
			_file: core.#ReadFile & {
				input: _dockerfile
				path:  "Dockerfile.daemon"
			}
			source: _imageSource
			dockerfile: contents: _file.contents
		}

		verify: testing.#Image & {
			testImage:  image.output
			dockerHost: client.network."unix:///var/run/docker.sock".connect
		}

		push: utils.#Push & {
			env: {
				AWS_ACCOUNT_ID: 	client.env.AWS_ACCOUNT_ID
				AWS_ECR_SECRET: 	client.commands.aws.stdout
				AWS_REGION: 		client.env.AWS_REGION
				DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
				DOCKERHUB_TOKEN: 	client.env.DOCKERHUB_TOKEN
			}
			params: {
				envTag:   version.envTag
				branch:   "develop" //version.branch
				repo:     version.repo
				repoType: version.repoType
				sha:      version.sha
				shaTag:   version.shaTag
				image:    actions.image.output
			}
		}

		queue: [Region=string]: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: {
			_queue: utils.#Queue & {
				env: {
					AWS_ACCOUNT_ID:        client.env.AWS_ACCOUNT_ID
					AWS_ACCESS_KEY_ID:     client.env.AWS_ACCESS_KEY_ID
					AWS_SECRET_ACCESS_KEY: client.env.AWS_SECRET_ACCESS_KEY
					AWS_REGION: 		   "\(Region)"
				}
				params: {
					event:  "deploy"
					repo:   "js-ceramic"
					envTag: "\(EnvTag)"
					branch: "\(Branch)"
					sha:    "\(Sha)"
					shaTag: "\(ShaTag)"
				}
			}
		}
	}
}
