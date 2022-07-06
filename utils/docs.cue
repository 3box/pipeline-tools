package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

#Docs: {
	src: dagger.#FS
	ver: int | *16

	_node: docker.#Pull & {
		source: "node:\(ver)"
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
			npm run build
			npm run docs
		"""#
	}
}
