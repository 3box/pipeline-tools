package utils

import (
	"encoding/json"

	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/aws/cli"
)

#Queue: {
	env: {
		AWS_ACCOUNT_ID:        string
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		AWS_REGION: 		   aws.#Region
	}

	params: {
		event:    "deploy" | "smoke" | "e2e" | "anchor"
		envTag:   "dev" | "qa" | "tnet" | "prod"
		repo:     "js-ceramic" | "ceramic-anchor-service" | "go-ipfs-daemon"
		branch:   "dev" | "develop" | "release-candidate" | "tnet" | "main" | "master"
		sha:      string
		shaTag:   string
	}

	send: cli.#Command & {
		credentials: aws.#Credentials & {
			accessKeyId:     env.AWS_ACCESS_KEY_ID
			secretAccessKey: env.AWS_SECRET_ACCESS_KEY
		}
		options: region: "\(env.AWS_REGION)"
		service: {
			name:    "sqs"
			command: "send-message"
			args: [
				"--queue-url",
				"https://sqs.\(env.AWS_REGION).amazonaws.com/\(env.AWS_ACCOUNT_ID)/ceramic-ci-\(params.envTag).fifo",
				"--message-body",
				"\"\(json.Marshal(params))\"",
				"--message-group-id",
				"\(params.event)"
			]
		}
	}
}
