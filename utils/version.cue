package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

#Version: {
	src: dagger.#FS

    _cli: alpine.#Build & {
        packages: {
            bash: _
            git:  _
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
            if [[ "$BRANCH" == 'main' || "$BRANCH" == 'master' || "$BRANCH" == 'prod' ]]; then
                ENV_TAG='prod'
            elif [[ "$BRANCH" == 'release-candidate' || "$BRANCH" == 'rc' || "$BRANCH" == 'tnet' ]]; then
                ENV_TAG='tnet'
            else
                ENV_TAG='dev'
            fi

            REPO=$(basename $(git config --get remote.origin.url) .git)
            if [[ "$REPO" == *"ceramic"* ]]; then
                REPO_TYPE='ceramic'
            elif [[ "$REPO" == *"anchor"* ]]; then
                REPO_TYPE='cas'
            elif [[ "$REPO" == *"ipfs"* ]]; then
                REPO_TYPE='ipfs'
            fi

            echo -n $ENV_TAG                      > /envTag
            echo -n $REPO                         > /repo
            echo -n $REPO_TYPE                    > /repoType
            echo -n $BRANCH                       > /branch
            echo -n $(git rev-parse HEAD)         > /sha
            echo -n $(git rev-parse --short HEAD) > /shaTag
            echo -n $(git log -1 --pretty=%B)     > /message
        """#
        export: files: {
            "/envTag":	 string
            "/repo":	 string
            "/repoType": string
            "/branch":   string
            "/sha":      string
            "/shaTag":   string
            "/message":  string
        }
    }
    envTag:   run.export.files["/envTag"]
    repo:     run.export.files["/repo"]
    repoType: run.export.files["/repoType"]
    branch:   run.export.files["/branch"]
    sha:      run.export.files["/sha"]
    shaTag:   run.export.files["/shaTag"]
    message:  run.export.files["/message"]
}
