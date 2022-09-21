package plans

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"

	"github.com/3box/pipeline-tools/ci/utils"
)

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

	actions: {
		image: docker.#Dockerfile & {
			source: client.filesystem.source.read.contents
		}
		push: [Region=aws.#Region]: [Sha=#Sha]: [ShaTag=#ShaTag]: {
			utils.#ECR & {
				img: image.output
				env: {
					AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
					AWS_ECR_SECRET: client.commands.aws.stdout
					AWS_REGION:     Region
					REPO:           "ceramic-qa-tests-e2e_tests"
					TAGS: ["\(Sha)", "\(ShaTag)", "latest"]
				}
			}
		}
	}
}
