package ceramic

import (
	"dagger.io/dagger"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
//	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: {
		env: {
			BRANCH:                string
			GITHUB_SHA:            string
			AWS_ACCESS_KEY_ID:     dagger.#Secret
			AWS_SECRET_ACCESS_KEY: dagger.#Secret
			AWS_DEFAULT_REGION:    dagger.#Secret
			DOCKERHUB_USERNAME:    dagger.#Secret
			DOCKERHUB_TOKEN:       dagger.#Secret
		}
		filesystem: {
			"./": read: contents: dagger.#FS
			".":  read: {
				contents: dagger.#FS
				include: [
					"Dockerfile.daemon",
					"package.json",
					"package-lock.json",
					"lerna.json",
					"tsconfig.json",
					"packages",
					"types"
				]
			}
		}
		network: "unix:///var/run/docker.sock": connect: dagger.#Socket
	}
	actions: {
		_source:      client.filesystem["./"].read.contents
		_imageSource: client.filesystem["."].read.contents

		test: docker.#Build & {
			steps: [
				docker.#Pull & {
					source: "node:16"
				},
				docker.#Copy & {
					contents: _source
					dest:     "./src"
				},
			  bash.#Run & {
			  	workdir: "./src"
			  	script: contents: #"""
			  		npm ci
			  		npm run build
			  		npm run test
			  	"""#
			  	}
			]
		}
    push: {
      _image: docker.#Dockerfile & {
      	source: _imageSource
      	dockerfile: path: "Dockerfile.daemon"
      }
      _pushDH: docker.#Push & {
      	image: _image.output
      	dest: "js-ceramic:debug"
      }
      _pushECR: docker.#Push & {
      	image: _image.output
      	dest: "js-ceramic:debug"
      }
    }
	}
}
