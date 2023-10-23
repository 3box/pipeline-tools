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
	_deltaDays: string | *"+0"
	_cli:       alpine.#Build & {
		packages: {
			bash: {}
			coreutils: {}
		}
	}
	run: bash.#Run & {
		input:  _cli.output
		always: true
		env: DELTA_DAYS: _deltaDays
		script: contents: #"""
				echo -n $(date +%s%N -d "$DELTA_DAYS days") > /epochNs
				echo -n $(date +%s   -d "$DELTA_DAYS days") > /epochS
			"""#
		export: files: {
			"/epochNs": string
			"/epochS":  string
		}
	}
	epochNs: run.export.files["/epochNs"]
	epochS:  run.export.files["/epochS"]
}

#Job: {
	env: {
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		AWS_REGION:            aws.#Region
		ENV_TAG:               string
	}

	spec: {
		type: "deploy" | "anchor" | "test_e2e" | "test_smoke"
		params: {
			component: "ceramic" | "cas" | "ipfs" | *null
			sha:       string | *null
			shaTag:    string | *null
			version:   string | *null
		}
	}

	_uuid: #Uuid
	_ts:   #EpochTs
	_ttl:  #EpochTs & {
		_deltaDays: "+14" // Two weeks
	}
	_spec: {
		type: {
			S: spec.type
		}
		stage: {
			S: "queued"
		}
		id: {
			S: "\(_uuid.uuid)"
		}
		job: {
			S: "\(_uuid.uuid)"
		}
		ts: {
			N: "\(_ts.epochNs)"
		}
		ttl: {
			N: "\(_ttl.epochS)"
		}
		params: {
			M: {
				if spec.params.component != null {
					component: {
						S: spec.params.component
					}
				}
				if spec.params.sha != null {
					sha: {
						S: spec.params.sha
					}
				}
				if spec.params.shaTag != null {
					shaTag: {
						S: spec.params.shaTag
					}
				}
				if spec.params.version != null {
					version: {
						S: spec.params.version
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
			region: "\(env.AWS_REGION)"
		}
		unmarshal: false
		service: {
			name:    "dynamodb"
			command: "put-item"
			args: [
				"--table-name",
				"ceramic-\(env.ENV_TAG)-ops",
				"--item",
				"'\(json.Marshal(_spec))'",
			]
		}
	}
}
