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
		ENV_TAG:			   string
	}

	params: {
		event:    	"deploy"
		component:  "ceramic" | "cas" | "ipfs"
		sha:      	string
		shaTag:   	string
		version:	string | *""
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
				"https://sqs.\(env.AWS_REGION).amazonaws.com/\(env.AWS_ACCOUNT_ID)/ceramic-\(env.ENV_TAG)-ops.fifo",
				"--message-body",
				"\"\(json.Marshal(params))\"",
				"--message-group-id",
				"\(params.event)"
			]
		}
	}
}
