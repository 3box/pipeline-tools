package ci

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"

	"github.com/3box/pipeline-tools/utils"
)

#Branch: "develop" | "release-candidate" | "main"
#EnvTag: "dev" | "qa" | "tnet" | "prod"
#Sha:	 =~"[0-9a-f]{40}"
#ShaTag: =~"[0-9a-f]{12}"

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
	client: filesystem: fullSource: read: {
		path: "."
		contents: dagger.#FS
		exclude: [
			".github",
			"cue.mod",
			"node_modules",
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
		_repo:			"js-ceramic"
		_fullSource:	client.filesystem.fullSource.read.contents
		_imageSource:	client.filesystem.imageSource.read.contents
		_dockerfile:	client.filesystem.dockerfile.read.contents

        testJs:  utils.#TestNode & {
			src: _fullSource
			run: env: IPFS_FLAVOR: "js"
		}

		testGo:  utils.#TestNode & {
			src: _fullSource
			run: env: IPFS_FLAVOR: "go"
		}

		_image: docker.#Dockerfile & {
			_file: core.#ReadFile & {
				input: _dockerfile
				path:  "Dockerfile.daemon"
			}
			source: _imageSource
			dockerfile: contents: _file.contents
		}

		verify: utils.#TestImage & {
			testImage:  _image.output
			endpoint:	"api/v0/node/healthcheck"
			port:		7007
			dockerHost: client.network."unix:///var/run/docker.sock".connect
		}

		push: [Region=aws.#Region]: [EnvTag=#EnvTag]: [Branch=#Branch]: [Sha=#Sha]: [ShaTag=#ShaTag]: {
			_baseTags: ["\(EnvTag)", "\(Branch)", "\(Sha)", "\(ShaTag)"]
			_tags:	   [...string]
			{
				Branch == "main"
				_tags: _baseTags + ["latest"]
			} | {
				_tags: _baseTags
			}
			ecr: {
				if Branch == "develop" {
					qa: utils.#ECR & {
						img: _image.output
						env: {
							AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
							AWS_ECR_SECRET: client.commands.aws.stdout
							AWS_REGION: 	Region
							REPO:			"ceramic-qa"
							TAGS:			_baseTags + ["qa"]
						}
					}
				}
				utils.#ECR & {
					img: _image.output
					env: {
						AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
						AWS_ECR_SECRET: client.commands.aws.stdout
						AWS_REGION: 	Region
						REPO:			"ceramic-\(EnvTag)"
						TAGS:			_tags
					}
				}
			}
			dockerhub: utils.#Dockerhub & {
				img: _image.output
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN: 	client.env.DOCKERHUB_TOKEN
					REPO:				_repo
					TAGS:				_tags
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

		docs: utils.#Docs & {
			src: _fullSource
			run: env: NODE_OPTIONS: "--max_old_space_size=3584"
		}
	}
}
