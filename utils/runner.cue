package utils

import (
    "universe.dagger.io/alpine"
    "universe.dagger.io/docker"
)

#Runner: {
	ver: string
	docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: {
					bash: 		  {}
					curl: 		  {}
					"docker-cli": {}
					jq: 		  {}
					unzip: 		  {}
				}
			},
			docker.#Run & {
				env: DAGGER_VERSION: "\(ver)"
				command: {
					name: "sh"
					flags: "-c": #"""
						curl -fsSL https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=$DAGGER_VERSION sh
						curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
						unzip -q awscliv2.zip
						./aws/install
					"""#
				}
			},
		]
	}
}
