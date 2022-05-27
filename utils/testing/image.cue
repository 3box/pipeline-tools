package testing

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"
)

#Image: {
	testImage:		docker.#Image
	dockerHost:		dagger.#Socket
	_testImageName: "ci-test-image"

	run: {
		_loadImage: cli.#Load & {
			image:    testImage
			host:     dockerHost
			tag:      _testImageName
		}
		startImage: cli.#Run & {
			env: {
				IMAGE_NAME: _testImageName
				PORTS:      "7007:7007"
				DEP:        "\(_loadImage.success)"
			}
			host:   dockerHost
			always: true
			command: {
				name: "sh"
				flags: "-c": #"""
					docker run -d --rm --name "$IMAGE_NAME" -p "$PORTS" "$IMAGE_NAME"
				"""#
			}
		}
		_cli: alpine.#Build & {
			packages: {
				bash: {}
				curl: {}
			}
		}
		healthcheck: bash.#Run & {
			env: {
				URL:     "http://0.0.0.0:7007/api/v0/node/healthcheck"
				TIMEOUT: "60"
				DEP:     "\(startImage.success)"
			}
			input: _cli.output
			always: true
			script: contents: #"""
				timeout=$TIMEOUT
				until [[ $timeout -le 0 ]]; do
					echo Waiting for Ceramic daemon to start...
					curl --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

					if grep -q "Alive!" curl.out
					then
						echo Healthcheck passed
						exit 0
					fi

					sleep 1
					timeout=$(( timeout - 1 ))
				done

				if [ $timeout -le 0 ]; then
					echo Healthcheck failed
					exit 1
				fi
			"""#
		}
		_clean: cli.#Run & {
			env: {
				IMAGE_NAME: _testImageName
				DEP:        "\(healthcheck.success)"
			}
			host:   dockerHost
			always: true
			command: {
				name: "sh"
				flags: "-c": #"""
					docker rm --force "$IMAGE_NAME"
				"""#
			}
		}
	}
}
