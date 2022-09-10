package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

#Status: {
	status:    "pending" | "failure" | "success"
	runUrl:    string
	statusUrl: string
	token:     dagger.#Secret
	_cli:      alpine.#Build & {
		packages: {
			bash: {}
			curl: {}
		}
	}
	run: bash.#Run & {
		env: {
			STATUS:     status
			RUN_URL:    runUrl
			STATUS_URL: statusUrl
			GH_TOKEN:   token
		}
		input:  _cli.output
		always: true
		script: contents: #"""
				desc="failed"
				if [[ $STATUS == 'pending' ]]; then
					desc="started"
				elif [[ $STATUS == 'success' ]]; then
					desc="successful"
				fi
				res=$(curl \
						-X POST \
						-H "Accept: application/vnd.github.v3+json" \
						-H "Authorization: token $GH_TOKEN" \
						$STATUS_URL \
						-d "{\"state\":\"$STATUS\",\"target_url\":\"$RUN_URL\",\"description\":\"Image verification $desc\",\"context\":\"ci/image: verify\"}")
				echo $res
				if [[ $res != *"$STATUS"* ]]; then
					exit 1
				fi
			"""#
	}
}
