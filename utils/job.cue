package utils

import (
	"encoding/json"

	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/aws"
	"universe.dagger.io/aws/cli"
	"universe.dagger.io/bash"
)

#Uuid: {
	_cli: alpine.#Build & {
		packages: {
			bash: {}
			uuidgen: {}
		}
	}
	run: bash.#Run & {
		input:  _cli.output
		always: true
		script: contents: #"""
				echo -n $(uuidgen) > /uuid
			"""#
		export: files: {
			"/uuid": string
		}
	}
	uuid: run.export.files["/uuid"]
}

#EpochTs: {
	_cli: alpine.#Build & {
		packages: {
			bash: {}
		}
	}
	run: bash.#Run & {
		input:  _cli.output
		always: true
		script: contents: #"""
				echo -n "$(date +%s)000" > /epochTs
			"""#
		export: files: {
			"/epochTs":  string
		}
	}
	epochTs: run.export.files["/epochTs"]
}

#Job: {
	env: {
		AWS_ACCOUNT_ID:        string
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		AWS_REGION:            aws.#Region
		ENV_TAG:               string
	}

	job: {
		type:   "deploy" | "anchor" | "test_e2e" | "test_smoke"
		params: {
			component: "ceramic" | "cas" | "ipfs" | *null
			sha:       string | *null
			shaTag:    string | *null
			version:   string | *null
		}
	}

	_uuid: #Uuid
	_epochTs: #EpochTs
	_job: {
		type: {
			S: job.type
		}
		stage: {
			S: "queued"
		}
		id: {
			S: "\(_uuid.uuid)"
		}
		ts: {
			N: "\(_epochTs.epochTs)"
		}
		params: {
			M: {
				if job.params.component != null {
					component: {
						S: job.params.component
					}
				}
				if job.params.sha != null {
					sha: {
						S: job.params.sha
					}
				}
				if job.params.shaTag != null {
					shaTag: {
						S: job.params.shaTag
					}
				}
				if job.params.version != null {
					version: {
						S: job.params.version
					}
				}
			}
		}
	}

	write: cli.#Command & {
		credentials: aws.#Credentials & {
			accessKeyId:     env.AWS_ACCESS_KEY_ID
			secretAccessKey: env.AWS_SECRET_ACCESS_KEY
		}
		options: {
			region:    "\(env.AWS_REGION)"
		}
		unmarshal: false
		service: {
			name:    "dynamodb"
			command: "put-item"
			args: [
				"--table-name",
				"ceramic-\(env.ENV_TAG)-ops",
				"--item",
				"'\(json.Marshal(_job))'"
			]
		}
	}
}
