package plans

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"

	"github.com/3box/pipeline-tools/ci/utils"
)

#Branch: "develop" | "qa" | "tnet" | "main"
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
		// Runtime
		DAGGER_LOG_FORMAT: string | *"plain"
		DAGGER_LOG_LEVEL:  string | *"info"
		ENV_TAG:           #EnvTag
	}
	client: commands: aws: {
		name: "aws"
		args: ["ecr", "get-login-password"]
		stdout: dagger.#Secret
	}
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
		image: docker.#Dockerfile & {
			buildArg: "ENV_TAG": client.env.ENV_TAG
			target: "target"
			source: client.filesystem.source.read.contents
		}

		verify: utils.#TestLocalstack & {
			testImage:  image.output
			endpoint:   "/healthcheck"
			port:       8080
			timeout:    60
			dockerHost: client.network."unix:///var/run/docker.sock".connect
		}

		push: [Region=aws.#Region]: [EnvTag=#EnvTag]: [Branch=#Branch]: [Sha=#Sha]: [ShaTag=#ShaTag]: {
			_tags: ["\(EnvTag)", "\(Branch)", "\(Sha)", "\(ShaTag)"]
			_extraTags: [...string] | *[]
			if EnvTag == "prod" {
				_extraTags: ["latest"]
			}
			ecr: utils.#ECR & {
				img: image.output
				env: {
					AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
					AWS_ECR_SECRET: client.commands.aws.stdout
					AWS_REGION:     Region
					REPO:           "ceramic-prod-ops-cd-manager"
					TAGS:           _tags + _extraTags
				}
			}
		}
	}
}
