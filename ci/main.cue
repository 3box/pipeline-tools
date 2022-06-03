package ci

import (
    "dagger.io/dagger"

    "universe.dagger.io/bash"

	"github.com/3box/pipeline-tools/utils"
)

dagger.#Plan & {
    client: env: {
        // Secrets
        AWS_ACCOUNT_ID:        dagger.#Secret
        AWS_REGION:            dagger.#Secret
        AWS_ACCESS_KEY_ID:     dagger.#Secret
        AWS_SECRET_ACCESS_KEY: dagger.#Secret
        DOCKERHUB_USERNAME:    dagger.#Secret
        DOCKERHUB_TOKEN:       dagger.#Secret
        // Runtime
        COMPONENT:             utils.#Component
        PIPELINE_TOOLS_VER:    string | *"develop"
        DAGGER_LOG_FORMAT:     string | *"plain"
        DAGGER_LOG_LEVEL:      string | *"info"
        DAGGER_VERSION:        string | *"0.2.14"
        DAGGER_CACHE_TO:       string | *""
        DAGGER_CACHE_FROM:     string | *""
        GITHUB_ACTIONS:        string | *""
        ACTIONS_CONTEXT:       string | *""
        ACTIONS_RUNTIME_TOKEN: string | *""
        ACTIONS_CACHE_URL:     string | *""
    }
    // Full source to use for building/testing code.
    client: filesystem: source: read: {
        path: "."
        contents: dagger.#FS
        exclude: ["cue.mod"]
    }
    client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

    actions: {
        _runner: utils.#Runner & {
        	ver: client.env.DAGGER_VERSION
        }
        build: bash.#Run & {
            env: {
                // Secrets
                AWS_ACCOUNT_ID:        client.env.AWS_ACCOUNT_ID
                AWS_REGION:            client.env.AWS_REGION
                AWS_ACCESS_KEY_ID:     client.env.AWS_ACCESS_KEY_ID
                AWS_SECRET_ACCESS_KEY: client.env.AWS_SECRET_ACCESS_KEY
                DOCKERHUB_USERNAME:    client.env.DOCKERHUB_USERNAME
                DOCKERHUB_TOKEN:       client.env.DOCKERHUB_TOKEN
                // Runtime
                COMPONENT:             client.env.COMPONENT
                PIPELINE_TOOLS_VER:	   client.env.PIPELINE_TOOLS_VER
                DAGGER_PLAN:           "cue.mod/pkg/github.com/3box/pipeline-tools/ci/\(client.env.COMPONENT).cue"
                DAGGER_LOG_FORMAT:     client.env.DAGGER_LOG_FORMAT
                DAGGER_LOG_LEVEL:      client.env.DAGGER_LOG_LEVEL
                DAGGER_VERSION:        client.env.DAGGER_VERSION
                GITHUB_ACTIONS:        client.env.GITHUB_ACTIONS
                GITHUB_CONTEXT:        client.env.ACTIONS_CONTEXT
                ACTIONS_RUNTIME_TOKEN: client.env.ACTIONS_RUNTIME_TOKEN
                ACTIONS_CACHE_URL:     client.env.ACTIONS_CACHE_URL
            }
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
                dagger project init
                dagger project update

                # TODO: Report bug to Dagger. Script doesn't get automatically copied during `dagger update` and also
                # doesn't have executable permissions.
                AWS_SCRIPTS=cue.mod/pkg/universe.dagger.io/aws/_scripts
                mkdir -p $AWS_SCRIPTS
                curl -fsSL "https://raw.githubusercontent.com/dagger/dagger/v$DAGGER_VERSION/pkg/universe.dagger.io/aws/_scripts/install.sh" > $AWS_SCRIPTS/install.sh
                chmod +x $AWS_SCRIPTS/install.sh

                if [[ "$PIPELINE_TOOLS_VER" != 'develop' ]]; then
                    PIPELINE_TOOLS_VER="v$PIPELINE_TOOLS_VER"
                fi
                dagger project update "github.com/3box/pipeline-tools@$PIPELINE_TOOLS_VER"

                dagger do test -p $DAGGER_PLAN
                dagger do verify -p $DAGGER_PLAN

                # The JSON output of the `build` action provides additional repository metadata to be used subsequently.
                REPO_META=$(dagger do --output-format json version -p $DAGGER_PLAN)
                echo $REPO_META
                PUBLISH=$(echo "${REPO_META}" | jq -r '.publish')

                if [[ "$PUBLISH" == 'true' ]]; then
                    ENV_TAG=$(echo "${REPO_META}" | jq -r '.envTag')
                    REPO=$(echo "${REPO_META}" | jq -r '.repo')
                    BRANCH=$(echo "${REPO_META}" | jq -r '.branch')
                    SHA=$(echo "${REPO_META}" | jq -r '.sha')
                    SHA_TAG=$(echo "${REPO_META}" | jq -r '.shaTag')

                    dagger do push -w "actions:push:\"$AWS_REGION\":\"$ENV_TAG\":\"$BRANCH\":\"$SHA\":\"$SHA_TAG\":_" -p $DAGGER_PLAN
                    dagger do queue -w "actions:queue:\"$AWS_REGION\":\"$ENV_TAG\":\"$BRANCH\":\"$SHA\":\"$SHA_TAG\":_" -p $DAGGER_PLAN
                fi
            """#
        }
    }
}
