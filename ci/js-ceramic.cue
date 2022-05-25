package ceramic

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"

	"github.com/3box/pipelinetools/utils"
)

dagger.#Plan & {
	client: env: {
		// Secrets
		AWS_ACCOUNT_ID:        string
		AWS_DEFAULT_REGION:    string
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		DOCKERHUB_USERNAME:    string
		DOCKERHUB_TOKEN:       dagger.#Secret
		// Runtime
		DAGGER_LOG_FORMAT:     string | *"auto"
		ACTIONS_RUNTIME_TOKEN: string | *""
		ACTIONS_CACHE_URL:     string | *""
	}
	client: commands: aws: {
		name: "aws"
		args: ["ecr", "get-login-password"]
		stdout: dagger.#Secret
	}
	// Full source to use for building/testing code
	client: filesystem: fullSource: read: {
		path: "."
		contents: dagger.#FS
		exclude: [
			".github",
			"cue.mod",
		]
	}
	// Subset of source required to build Docker image
	client: filesystem: imageSource: read: {
		path: "."
		contents: dagger.#FS
		include: [
			"package.json",
			"package-lock.json",
			"lerna.json",
			"tsconfig.json",
			"packages",
			"types"
		]
	}
	// Dockerfile
	client: filesystem: dockerfile: read: {
		path: "."
		contents: dagger.#FS
		include: ["Dockerfile.daemon"]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_testImageName: "js-ceramic-ci"

		_unitTest: {
			_node: docker.#Pull & {
				source: "node:16"
			}
			run: bash.#Run & {
				env:     IPFS_FLAVOR: "js" | *"go"
				input:   _node.output
				workdir: "./src"
				mounts:  source: {
					dest:     "/src"
					contents: client.filesystem.fullSource.read.contents
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

					echo -n $(git rev-parse HEAD) > /sha
					echo -n $(git rev-parse --short HEAD) > /shaTag
					echo -n $(git log -1 --pretty=%B) > /message
					echo -n $BRANCH > /branch
					echo -n $ENV_TAG > /envTag

					npm run lint
					npm ci
					npm run build
					npm run test
				"""#
				export: files: {
					"/sha":     string
					"/shaTag":  string
					"/branch":  string
					"/message": string
					"/envTag":	string
				}
			}
			sha:     run.export.files["/sha"]
			shaTag:  run.export.files["/shaTag"]
			branch:  run.export.files["/branch"]
			message: run.export.files["/message"]
			envTag:  run.export.files["/envTag"]
		}

		build: {
			// These steps can be run in parallel in separate containers
			testJs:  _unitTest & {
				run: env: IPFS_FLAVOR: "js"
				run: env: NODE_OPTIONS: "--max_old_space_size=4096"
			}
			testGo:  _unitTest & {
				run: env: IPFS_FLAVOR: "go"
			}
			sha:     testGo.sha
			shaTag:  testGo.shaTag
			branch:  testGo.branch
			message: testGo.message
			envTag:  testGo.envTag
		}

		_clean: cli.#Run & {
			host:   client.network."unix:///var/run/docker.sock".connect
			always: true
			env: IMAGE_NAME: _testImageName
			command: {
				name: "sh"
				flags: "-c": #"""
					docker rm --force "$IMAGE_NAME"
				"""#
			}
		}

		verify: {
			buildImage: docker.#Dockerfile & {
				_dockerfile: core.#ReadFile & {
					input: client.filesystem.dockerfile.read.contents
					path: "Dockerfile.daemon"
				}
				source: client.filesystem.imageSource.read.contents
				dockerfile: contents: _dockerfile.contents
			}
			testImage: {
				_preload: _clean
				_loadImage: cli.#Load & {
					env: DEP: "\(_preload.success)"
					image:    buildImage.output
					host:     client.network."unix:///var/run/docker.sock".connect
					tag:      _testImageName
				}
				startImage: cli.#Run & {
					host:   client.network."unix:///var/run/docker.sock".connect
					always: true
					env: {
						IMAGE_NAME: _testImageName
						PORTS:      "7007:7007"
						DEP:        "\(_loadImage.success)"
					}
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
						URL:     "http://0.0.0.0:7007/api/v0/node/healthcheck"
						TIMEOUT: "60"
						DEP:     "\(startImage.success)"
					}
					input: _cli.output
					always: true
					script: contents: #"""
						timeout=$TIMEOUT
						until [[ $timeout -le 0 ]]; do
							echo Waiting for Ceramic daemon to start...
							curl --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

							if grep -q "Alive!" curl.out
							then
								echo Healthcheck passed
								exit 0
							fi

							sleep 1
							timeout=$(( timeout - 1 ))
						done

						if [ $timeout -le 0 ]; then
							echo Healthcheck failed
							exit 1
						fi
					"""#
				}
				_postload: _clean & {
					env: DEP: "\(healthcheck.success)"
				}
			}
			output: buildImage.output
		}

		push: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: {
			tags: [...string]

			for tag in [EnvTag, Branch, Sha, ShaTag] {
				"dockerhub_\(tag)":  docker.#Push & {
					image: verify.output
					dest:  "ceramicnetwork/js-ceramic:\(tag)"
					auth: {
						username: client.env.DOCKERHUB_USERNAME
						secret: client.env.DOCKERHUB_TOKEN
					}
				}
				"ecr_\(tag)":  docker.#Push & {
					image: verify.buildImage.output
					dest:  "\(client.env.AWS_ACCOUNT_ID).dkr.ecr.\(client.env.AWS_DEFAULT_REGION).amazonaws.com/ceramic-\(EnvTag):\(tag)"
					auth: {
						username: "AWS"
						secret: client.commands.aws.stdout
					}
				}
			}
		}

		queue: [Region=string]: [EnvTag=string]: [Branch=string]: [Sha=string]: [ShaTag=string]: {
			_queue: utils.#Queue & {
				env: {
					AWS_ACCOUNT_ID:        client.env.AWS_ACCOUNT_ID
					AWS_ACCESS_KEY_ID:     client.env.AWS_ACCESS_KEY_ID
					AWS_SECRET_ACCESS_KEY: client.env.AWS_SECRET_ACCESS_KEY
					AWS_REGION: 		   "\(Region)"
				}
				params: {
					event:  "deploy"
					repo:   "js-ceramic"
					envTag: "\(EnvTag)"
					branch: "\(Branch)"
					sha:    "\(Sha)"
					shaTag: "\(ShaTag)"
				}
			}
		}
	}
}
