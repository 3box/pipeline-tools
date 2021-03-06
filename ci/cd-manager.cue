package ci

import (
	"dagger.io/dagger"

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
		// Runtime
		DAGGER_LOG_FORMAT:     string | *"plain"
		DAGGER_LOG_LEVEL:      string | *"info"
	}
	client: commands: aws: {
		name: "aws"
		args: ["ecr", "get-login-password"]
		stdout: dagger.#Secret
	}
	client: filesystem: source: read: {
		path: "."
		contents: dagger.#FS
		exclude: [
			"target",
		]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_image: docker.#Dockerfile & {
			source: client.filesystem.source.read.contents
		}

		verify: utils.#TestImage & {
			testImage:  _image.output
			endpoint:	"/health"
			port:		8000
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
							REPO:			"ceramic-qa-ops-cd-manager"
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
						REPO:			"ceramic-\(EnvTag)-ops-cd-manager"
						TAGS:			_tags
					}
				}
			}
		}
	}
}
