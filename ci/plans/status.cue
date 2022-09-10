package plans

import (
	"dagger.io/dagger"

	"github.com/3box/pipeline-tools/ci/utils"
)

dagger.#Plan & {
	client: env: {
		// Secrets
		GH_TOKEN: dagger.#Secret
		// Runtime
		DAGGER_LOG_FORMAT: string | *"plain"
		DAGGER_LOG_LEVEL:  string | *"info"
		RUN_URL:           string
		STATUS_URL:        string
	}

	actions: {
		pending: utils.#Status & {
			status:    "pending"
			runUrl:    client.env.RUN_URL
			statusUrl: client.env.STATUS_URL
			token:     client.env.GH_TOKEN
		}
		success: utils.#Status & {
			status:    "success"
			runUrl:    client.env.RUN_URL
			statusUrl: client.env.STATUS_URL
			token:     client.env.GH_TOKEN
		}
		failure: utils.#Status & {
			status:    "failure"
			runUrl:    client.env.RUN_URL
			statusUrl: client.env.STATUS_URL
			token:     client.env.GH_TOKEN
		}
	}
}
