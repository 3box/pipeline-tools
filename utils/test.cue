package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"
)

#TestNode: {
	src: dagger.#FS
	ver: int | *16

	_node: docker.#Pull & {
		source: "node:\(ver)"
	}
	run: bash.#Run & {
		env: NODE_OPTIONS: "--max_old_space_size=4096"
		input:   _node.output
		workdir: "./src"
		mounts: source: {
			dest:     "/src"
			contents: src
		}
		script: contents: #"""
				npm ci
				npm run lint
				npm run build
				npm run test
			"""#
	}
}

#TestImage: {
	testImage:     docker.#Image
	testImageName: string | *"ci-test-image"
	dockerHost:    dagger.#Socket
	endpoint:      string
	port:          int
	cmd:           *"GET" | "POST" | "PUT"

	run: {
		_loadImage: cli.#Load & {
			image: testImage
			host:  dockerHost
			tag:   testImageName
		}
		_cli: alpine.#Build & {
			packages: {
				bash: {}
				curl: {}
				"docker-cli": {}
			}
		}
		healthcheck: bash.#Run & {
			env: {
				URL:        "http://0.0.0.0:\(port)/\(endpoint)"
				TIMEOUT:    "60"
				CMD:        "\(cmd)"
				IMAGE_NAME: testImageName
				DEP:        "\(_loadImage.success)"
			}
			input:  _cli.output
			always: true
			mounts: docker: {
				contents: dockerHost
				dest:     "/var/run/docker.sock"
			}
			script: contents: #"""
					docker rm -f "$IMAGE_NAME"
					docker run -d --name "$IMAGE_NAME" -p "$PORTS" "$IMAGE_NAME"

					timeout=$TIMEOUT
					until [[ $timeout -le 0 ]]; do
						sleep 1

						echo -e "\n=============== Startup Logs ===============\n"
						docker logs --details --timestamps --tail 100 "$IMAGE_NAME"
						curl -X $CMD --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

						if grep -q "200 OK" curl.out
						then
							echo Healthcheck passed
							docker rm -f "$IMAGE_NAME"
							exit 0
						fi

						timeout=$(( timeout - 1 ))
					done

					if [ $timeout -le 0 ]; then
						echo Healthcheck failed
						cat curl.out
						docker rm -f "$IMAGE_NAME"
						exit 1
					fi
				"""#
		}
	}
}

#TestLocalstack: {
	testImage:     docker.#Image
	testImageName: string | *"ci-test-image"
	dockerHost:    dagger.#Socket
	endpoint:      string
	port:          int
	cmd:           *"GET" | "POST" | "PUT"
	timeout:       int | *60

	run: {
		_loadImage: cli.#Load & {
			image: testImage
			host:  dockerHost
			tag:   testImageName
		}
		_cli: alpine.#Build & {
			packages: {
				bash: {}
				curl: {}
				"docker-cli": {}
			}
		}
		_localstack: bash.#Run & {
			env: {
				TIMEOUT: "\(timeout)"
				DEP:     "\(_loadImage.success)"
			}
			input:  _cli.output
			always: true
			mounts: docker: {
				contents: dockerHost
				dest:     "/var/run/docker.sock"
			}
			script: contents: #"""
				docker rm -f localstack
				docker run -d --rm --name localstack -p 4566:4566 -p 4510-4559:4510-4559 localstack/localstack

				timeout=$TIMEOUT
				until [[ $timeout -le 0 ]]; do
					echo -e "\n=============== Localstack Logs ===============\n"
					docker logs --details --timestamps --tail 100 localstack > logs.out
					cat logs.out

					if grep -q "Ready." logs.out
					then
						echo Localstack ready
						exit 0
					fi

					sleep 1
					timeout=$(( timeout - 1 ))
				done

				if [ $timeout -le 0 ]; then
					echo Localstack startup failed
					cat logs.out
					docker rm -f localstack
					exit 1
				fi
				"""#
		}
		healthcheck: bash.#Run & {
			env: {
				IMAGE_NAME: testImageName
				PORTS:      "\(port):\(port)"
				URL:        "http://0.0.0.0:\(port)/\(endpoint)"
				CMD:        "\(cmd)"
				TIMEOUT:    "\(timeout)"
				DEP:        "\(_localstack.success)"
			}
			input:  _cli.output
			always: true
			mounts: docker: {
				contents: dockerHost
				dest:     "/var/run/docker.sock"
			}
			script: contents: #"""
				docker rm -f "$IMAGE_NAME"
				docker run -d --name "$IMAGE_NAME" -e AWS_ENDPOINT=http://localhost:4566/000000000000 -p "$PORTS" "$IMAGE_NAME"

				timeout=$TIMEOUT
				until [[ $timeout -le 0 ]]; do
					sleep 1

					echo -e "\n=============== Startup Logs ===============\n"
					docker logs --details --timestamps --tail 100 "$IMAGE_NAME"
					curl --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true
					cat curl.out

					if grep -q "200 OK" curl.out
					then
						echo Healthcheck passed
						docker rm -f "$IMAGE_NAME" localstack
						exit 0
					fi

					timeout=$(( timeout - 1 ))
				done

				if [ $timeout -le 0 ]; then
					echo Healthcheck failed
					cat curl.out
					docker rm -f "$IMAGE_NAME" localstack
					exit 1
				fi
				"""#
		}
	}
}

#TestNoop: {
	_cli: alpine.#Build & {
		packages: {
			bash: {}
		}
	}
	run: bash.#Run & {
		input: _cli.output
		script: contents: #"""
				echo "I'm a successful test!"
			"""#
	}
}
