package ceramic

import (
    "dagger.io/dagger"

    "universe.dagger.io/bash"
    "universe.dagger.io/docker"
)

dagger.#Plan & {
    client: env: {
        // Secret
        AWS_ACCOUNT_ID:        dagger.#Secret
        AWS_REGION:            dagger.#Secret
        AWS_ACCESS_KEY_ID:     dagger.#Secret
        AWS_SECRET_ACCESS_KEY: dagger.#Secret
        DOCKERHUB_USERNAME:    dagger.#Secret
        DOCKERHUB_TOKEN:       dagger.#Secret
        // Runtime
        DAGGER_LOG_FORMAT:     string | *"auto"
        DAGGER_VERSION:        string | *"0.2.12"
        REPO_TYPE:             "ceramic" | "cas" | "ipfs"
        ACTIONS_CONTEXT:       string | *""
        ACTIONS_RUNTIME_TOKEN: string | *""
        ACTIONS_CACHE_URL:     string | *""
    }
    // Full source to use for building/testing code.
    client: filesystem: source: read: {
        path: "."
        contents: dagger.#FS
    }
    client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

    actions: {
        _runner: docker.#Build & {
            steps: [
                docker.#Pull & {
                    source: "ubuntu:latest"
                },
                docker.#Run & {
                    command: {
                        name: "sh"
                        flags: "-c": #"""
                            apt-get -qqy update
                            apt-get install -qqy curl jq
                            curl -fsSL https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=$DAGGER_VERSION sh
                            curl -fsSL https://get.docker.com | sh
                        """#
                    }
                },
            ]
        }
        build: bash.#Run & {
	        env: DAGGER_PLAN: "cue.mod/pkg/github.com/3box/pipelinetools/ci/\(client.env.REPO_TYPE).cue"
            input: _runner.output
            workdir: "/src"
            mounts: source: {
                dest:     "/src"
                contents: client.filesystem.source.read.contents
            }
			mounts: docker: {
				contents: client.network."unix:///var/run/docker.sock".connect
				dest:     "/var/run/docker.sock"
			}
			script: contents: #"""
                ./bin/dagger project init
                ./bin/dagger project update

                # TODO: Report bug to Dagger. Script doesn't get automatically copied during `dagger update` and also
                # doesn't have executable permissions.
                AWS_SCRIPTS=cue.mod/pkg/universe.dagger.io/aws/_scripts
                mkdir -p $AWS_SCRIPTS
                curl -L https://raw.githubusercontent.com/dagger/dagger/v$DAGGER_VERSION/pkg/universe.dagger.io/aws/_scripts/install.sh > $AWS_SCRIPTS/install.sh
                chmod +x $AWS_SCRIPTS/install.sh

                # Pull the latest pipelinetools.
                # TODO: Once the code is stable, use a tagged version.
                ./bin/dagger project update github.com/3box/pipelinetools@develop

                # The JSON output of the `build` action provides additional repository metadata to be used subsequently.
                ./bin/dagger do --output-format json version -p $DAGGER_PLAN
                ENV_TAG=$(echo "${REPO_META}" | jq -r '.envTag')
                REPO=$(echo "${REPO_META}" | jq -r '.repo')
                BRANCH=$(echo "${REPO_META}" | jq -r '.branch')
                SHA=$(echo "${REPO_META}" | jq -r '.sha')
                SHA_TAG=$(echo "${REPO_META}" | jq -r '.shaTag')
                # TODO
                BRANCH=develop

                ./bin/dagger do test -p $DAGGER_PLAN

                ./bin/dagger do verify -p $DAGGER_PLAN

                ./bin/dagger do push -w "actions:push:\"$AWS_REGION\":\"$ENV_TAG\":\"$REPO\":\"$REPO_TYPE\":\"$BRANCH\":\"$SHA\":\"$SHA_TAG\":_" -p $DAGGER_PLAN

                ./bin/dagger do queue -w "actions:queue:\"$AWS_REGION\":\"$ENV_TAG\":\"$REPO\":\"$REPO_TYPE\":\"$BRANCH\":\"$SHA\":\"$SHA_TAG\":_" -p $DAGGER_PLAN
            """#
        }
    }
}
