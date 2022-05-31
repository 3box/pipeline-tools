package testing

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

#Node: {
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
        	pwd
            npm ci
            npm run lint
            npm run build
            npm run test
        """#
    }
}
