package ci

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"

	"github.com/3box/pipeline-tools/utils"
)

#Branch: "develop" | "release-candidate" | "main"
#EnvTag: "dev" | "qa" | "tnet" | "prod"
#Sha:    =~"[0-9a-f]{40}"
#ShaTag: =~"[0-9a-f]{12}"

dagger.#Plan & {
	client: env: {
		// Secrets
		AWS_ACCOUNT_ID:        string
		AWS_REGION:            string
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		DOCKERHUB_USERNAME:    string
		DOCKERHUB_TOKEN:       dagger.#Secret
		// Runtime
		DAGGER_LOG_FORMAT: string | *"plain"
		DAGGER_LOG_LEVEL:  string | *"info"
	}
	client: commands: aws: {
		name: "aws"
		args: ["ecr", "get-login-password"]
		stdout: dagger.#Secret
	}
	// Full source to use for building/testing code
	client: filesystem: source: read: {
		path:     "."
		contents: dagger.#FS
		exclude: [
			".github",
			"cue.mod",
		]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_repo:   "go-ipfs-daemon"
		_source: client.filesystem.source.read.contents

		test: utils.#TestNoop

		_image: docker.#Dockerfile & {
			source: _source
		}

		verify: utils.#TestImage & {
			testImage:  _image.output
			endpoint:   "api/v0/version"
			port:       5001
			cmd:        "POST"
			dockerHost: client.network."unix:///var/run/docker.sock".connect
		}

		push: [Region=aws.#Region]: [EnvTag=#EnvTag]: [Branch=#Branch]: [Sha=#Sha]: [ShaTag=#ShaTag]: {
			_baseTags: ["\(EnvTag)", "\(Branch)", "\(Sha)", "\(ShaTag)"]
			_tags: [...string]
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
							AWS_REGION:     Region
							REPO:           "go-ipfs-qa"
							TAGS:           _baseTags + ["qa"]
						}
					}
				}
				utils.#ECR & {
					img: _image.output
					env: {
						AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
						AWS_ECR_SECRET: client.commands.aws.stdout
						AWS_REGION:     Region
						REPO:           "go-ipfs-\(EnvTag)"
						TAGS:           _tags
					}
				}
			}
			dockerhub: utils.#Dockerhub & {
				img: _image.output
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN:    client.env.DOCKERHUB_TOKEN
					REPO:               _repo
					TAGS:               _tags
				}
			}
		}

		deploy: [Region=aws.#Region]: [EnvTag=string]: [Sha=#Sha]: [ShaTag=#ShaTag]: {
				jobEnv: {
					AWS_ACCOUNT_ID:        client.env.AWS_ACCOUNT_ID
					AWS_ACCESS_KEY_ID:     client.env.AWS_ACCESS_KEY_ID
					AWS_SECRET_ACCESS_KEY: client.env.AWS_SECRET_ACCESS_KEY
					AWS_REGION:            Region
				}
				jobParams: {
					type:   "deploy"
					params: {
						component: "ipfs"
						sha:       Sha
						shaTag:    ShaTag
					}
				}
			_deployEnv: utils.#Job & {
				env: jobEnv & {
					ENV_TAG: "\(EnvTag)"
				}
				job: jobParams
			}
			if EnvTag == "dev" {
				_deployQa: utils.#Job & {
					env: jobEnv & {
						ENV_TAG: "qa"
					}
					job: jobParams
				}
			}
		}
	}
}
