package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

#EnvTag: *"dev" | "qa" | "tnet" | "prod"

#Repo: "js-ceramic" | "ceramic-anchor-service" | "go-ipfs-daemon"

#Branch: "dev" | "develop" | "release-candidate" | "tnet" | "main" | "master"

#Component: "ceramic" | "cas" | "ipfs"

#Version: {
	src: dagger.#FS

    _cli: alpine.#Build & {
        packages: {
            bash: {}
            git:  {}
        }
    }

    run: bash.#Run & {
        input:   _cli.output
        workdir: "./src"
        mounts:  source: {
            dest:     "/src"
            contents: src
        }
        script:  contents: #"""
            BRANCH=$(git rev-parse --abbrev-ref HEAD)
            PUBLISH='false'
            if [[ "$BRANCH" == 'main' || "$BRANCH" == 'master' || "$BRANCH" == 'prod' ]]; then
                ENV_TAG='prod'
                PUBLISH='true'
            elif [[ "$BRANCH" == 'release-candidate' || "$BRANCH" == 'rc' || "$BRANCH" == 'tnet' ]]; then
                ENV_TAG='tnet'
                PUBLISH='true'
            elif [[ "$BRANCH" == 'dev' || "$BRANCH" == 'develop' ]]; then
                ENV_TAG='dev'
                PUBLISH='true'
            else
                ENV_TAG='dev'
            fi

            REPO=$(basename $(git config --get remote.origin.url) .git)
            if [[ "$REPO" == *"ceramic"* ]]; then
                COMPONENT='ceramic'
            elif [[ "$REPO" == *"anchor"* ]]; then
                COMPONENT='cas'
            elif [[ "$REPO" == *"ipfs"* ]]; then
                COMPONENT='ipfs'
            fi

            echo -n $ENV_TAG                      > /envTag
            echo -n $REPO                         > /repo
            echo -n $COMPONENT                    > /component
            echo -n $BRANCH                       > /branch
            echo -n $(git rev-parse HEAD)         > /sha
            echo -n $(git rev-parse --short HEAD) > /shaTag
            echo -n $(git log -1 --pretty=%B)     > /message
            echo -n $PUBLISH                      > /publish
        """#
        export: files: {
            "/envTag":	  string
            "/repo":	  string
            "/component": string
            "/branch":    string
            "/sha":       string
            "/shaTag":    string
            "/message":   string
            "/publish":   string
        }
    }
    envTag:    run.export.files["/envTag"]
    repo:      run.export.files["/repo"]
    component: run.export.files["/component"]
    branch:    run.export.files["/branch"]
    sha:       run.export.files["/sha"]
    shaTag:    run.export.files["/shaTag"]
    message:   run.export.files["/message"]
    publish:   run.export.files["/publish"]
}
