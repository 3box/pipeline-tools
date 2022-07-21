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
	testEnv: {
		AWS_ACCOUNT_ID?:        string
		AWS_REGION?:            string
		AWS_ACCESS_KEY_ID?:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY?: dagger.#Secret
		ENV?:                   "dev" | "tnet" | "prod"
	}

	run: {
		_loadImage: cli.#Load & {
			image: testImage
			host:  dockerHost
			tag:   testImageName
		}
		startImage: cli.#Run & {
			env: testEnv & {
				IMAGE_NAME: testImageName
				PORTS:      "\(port):\(port)"
				DEP:        "\(_loadImage.success)"
			}
			host:   dockerHost
			always: true
			command: {
				name: "sh"
				flags: "-c": #"""
						docker rm -f "$IMAGE_NAME"
						docker run -d \
							-e AWS_ACCOUNT_ID=$AWS_ACCOUNT_ID \
							-e AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID \
							-e AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY \
							-e AWS_REGION=$AWS_REGION \
							--name "$IMAGE_NAME" \
							-p "$PORTS" "$IMAGE_NAME"
					"""#
			}
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
				DEP:        "\(startImage.success)"
			}
			input:  _cli.output
			always: true
			mounts: docker: {
				contents: dockerHost
				dest:     "/var/run/docker.sock"
			}
			script: contents: #"""
					timeout=$TIMEOUT
					until [[ $timeout -le 0 ]]; do
						echo -e "\n=============== Startup Logs ===============\n"
						docker logs --details --timestamps --tail 100 "$IMAGE_NAME"
						curl -X $CMD --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

						if grep -q "200 OK" curl.out
						then
							echo Healthcheck passed
							docker rm -f "$IMAGE_NAME"
							exit 0
						fi

						sleep 1
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
