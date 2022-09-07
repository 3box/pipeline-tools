package ci

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"

	"github.com/3box/pipeline-tools/utils"
)

#EnvTag: "dev" | "qa" | "tnet" | "prod"
#Test: "test_e2e" | "test_smoke"

dagger.#Plan & {
	client: env: {
		// Secrets
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		// Runtime
		DAGGER_LOG_FORMAT: string | *"plain"
		DAGGER_LOG_LEVEL:  string | *"info"
	}

	actions: {
		test: [Region=aws.#Region]: [EnvTag=#EnvTag]: [Test=#Test]: utils.#Job & {
			env: {
				AWS_ACCESS_KEY_ID:     client.env.AWS_ACCESS_KEY_ID
				AWS_SECRET_ACCESS_KEY: client.env.AWS_SECRET_ACCESS_KEY
				AWS_REGION:            Region
				ENV_TAG:							 EnvTag
			}
			job: type: Test
		}
	}
}
