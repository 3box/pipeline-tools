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
        source: "node:16"
    }
    run: bash.#Run & {
        input:   _node.output
        workdir: "./src"
        mounts:  source: {
            dest:     "/src"
            contents: src
        }
        script:  contents: #"""
            npm ci
            npm run lint
            npm run build
            npm run test
        """#
    }
}

#TestImage: {
	testImage:		docker.#Image
	testImageName:  string | *"ci-test-image"
	dockerHost:		dagger.#Socket
	endpoint:		string
	port:			int
	cmd:			*"GET" | "POST" | "PUT"

	run: {
		_loadImage: cli.#Load & {
			image:    testImage
			host:     dockerHost
			tag:      testImageName
		}
		startImage: cli.#Run & {
			env: {
				IMAGE_NAME: testImageName
				PORTS:      "\(port):\(port)"
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
				URL:     "http://0.0.0.0:\(port)/\(endpoint)"
				TIMEOUT: "30"
				CMD:	 "\(cmd)"
				DEP:     "\(startImage.success)"
			}
			input: _cli.output
			always: true
			script: contents: #"""
				timeout=$TIMEOUT
				until [[ $timeout -le 0 ]]; do
					echo Waiting for image to start...
					curl -X $CMD --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

					if grep -q "200 OK" curl.out
					then
						echo Healthcheck passed
						exit 0
					fi

					sleep 1
					timeout=$(( timeout - 1 ))
				done

				if [ $timeout -le 0 ]; then
					echo Healthcheck failed
					cat curl.out
					exit 1
				fi
			"""#
		}
		_clean: cli.#Run & {
			env: {
				IMAGE_NAME: testImageName
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

#TestNoop: {
	_cli: alpine.#Build & {
		packages: {
			bash: {}
		}
	}
    run: bash.#Run & {
        input:  _cli.output
        script: contents: #"""
            echo "I'm a successful test!"
        """#
    }
}
