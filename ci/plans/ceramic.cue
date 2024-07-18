package plans

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpine"
	"universe.dagger.io/aws"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"

	"github.com/3box/pipeline-tools/ci/utils"
)

#Branch: "develop" | "qa" | "release-candidate" | "main"
#EnvTag: "dev" | "qa" | "tnet" | "prod"
#Sha:    =~"[0-9a-f]{40}"
#ShaTag: =~"[0-9a-f]{12}"

dagger.#Plan & {
	client: env: {
		// Secrets
		AWS_ACCOUNT_ID:        string
		AWS_REGION:            string
		AWS_ACCESS_KEY_ID:     dagger.#Secret
		AWS_SECRET_ACCESS_KEY: dagger.#Secret
		DOCKERHUB_USERNAME:    string
		DOCKERHUB_TOKEN:       dagger.#Secret
		// Runtime
		DAGGER_LOG_FORMAT: string | *"plain"
		DAGGER_LOG_LEVEL:  string | *"info"
	}
	client: commands: aws: {
		name: "aws"
		args: ["ecr", "get-login-password"]
		stdout: dagger.#Secret
	}
	// Full source to use for building/testing code
	client: filesystem: fullSource: read: {
		path:     "."
		contents: dagger.#FS
		exclude: [
			".github",
			"cue.mod",
			"node_modules",
		]
	}
	// Subset of source required to build Docker image
	client: filesystem: imageSource: read: {
		path:     "."
		contents: dagger.#FS
		include: [
			"ci-scripts",
			"package.json",
			"package-lock.json",
			"lerna.json",
			"tsconfig.json",
			"packages",
			"types",
		]
	}
	// Dockerfile
	client: filesystem: dockerfile: read: {
		path:     "."
		contents: dagger.#FS
		include: ["Dockerfile.daemon"]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_repo_ceramic:   "js-ceramic"
		_repo_composeDB: "composedb"
		_fullSource:     client.filesystem.fullSource.read.contents
		_imageSource:    client.filesystem.imageSource.read.contents
		_dockerfile:     client.filesystem.dockerfile.read.contents

		testJs: utils.#TestNode & {
			src: _fullSource
			run: env: IPFS_FLAVOR: "js"
		}

		testGo: utils.#TestNode & {
			src: _fullSource
			run: env: IPFS_FLAVOR: "go"
		}

		buildCeramic: docker.#Dockerfile & {
			_file: core.#ReadFile & {
				input: _dockerfile
				path:  "Dockerfile.daemon"
			}
			buildArg: "GIT_COMMIT_HASH": version.sha
			source: _imageSource
			dockerfile: contents: _file.contents
			target: "ceramic"
		}

		buildComposeDB: docker.#Dockerfile & {
			_file: core.#ReadFile & {
				input: _dockerfile
				path:  "Dockerfile.daemon"
			}
			buildArg: {
				"GIT_COMMIT_HASH": version.sha
				"BASE_BUILD":      buildCeramic.output.config.entrypoint[0] // Added to ensure the composeDB image waits for the js-ceramic image to be built
			}
			source: _imageSource
			dockerfile: contents: _file.contents
			target: "composedb"
		}
		verify: {
			verifyCeramic: {
				_dockerHost: client.network."unix:///var/run/docker.sock".connect
				_endpoint:  "api/v0/node/healthcheck"
				_port:      7007
				_imageName: "ci-test-image"
				_loadImage: cli.#Load & {
					image: buildCeramic.output
					host:  _dockerHost
					tag:   "\(_imageName)"
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
						URL:        "http://0.0.0.0:\(_port)/\(_endpoint)"
						TIMEOUT:    "60"
						IMAGE_NAME: "\(_imageName)"
						DEP:        "\(_loadImage.success)"
						PORTS:      "\(_port):\(_port)"
					}
					input:   _cli.output
					mounts: source: {
						dest:     "/var/run/docker.sock"
						contents: _dockerHost
					}
					mounts: docker: {
						contents: _dockerHost
						dest:     "/var/run/docker.sock"
					}
					always: true
					script: contents: #"""
							docker rm -f "$IMAGE_NAME" ceramic-one
							docker network create ci-test || true
							docker run -d --name ceramic-one \
								--platform linux/x86_64 \
								--network ci-test \
								-p 5101:5101 \
								-e CERAMIC_ONE_NETWORK='in-memory' \
								public.ecr.aws/r5b3e0r5/3box/ceramic-one:latest 
							docker run -d --platform linux/x86_64 --network ci-test \
								--name "$IMAGE_NAME" -p "$PORTS" "$IMAGE_NAME"

							timeout=$TIMEOUT
							until [[ $timeout -le 0 ]]; do
								echo -e "\n=============== Startup Logs ===============\n"
								docker logs --details --timestamps --tail 100 "$IMAGE_NAME"
								curl --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

								if grep -q "200 OK" curl.out
								then
									echo Healthcheck passed
									docker rm -f "$IMAGE_NAME" ceramic-one
									docker network rm ci-test
									exit 0
								fi

								sleep 1
								timeout=$(( timeout - 1 ))
							done

							if [ $timeout -le 0 ]; then
								echo Healthcheck failed
								docker rm -f "$IMAGE_NAME" ceramic-one
								docker network rm ci-test
								exit 1
							fi
						"""#
					}	
			}
			verifyComposeDB: utils.#TestImageCommand & {
				testImage: buildComposeDB.output
				command:   "composedb"
				commandArgs: ["version"]
			}
		}

		version: {
			_cli: alpine.#Build & {
				packages: {
					bash: {}
					git: {}
					npm: {}
				}
			}
			run: bash.#Run & {
				input:   _cli.output
				workdir: "./src"
				mounts: source: {
					dest:     "/src"
					contents: _fullSource
				}
				script: contents: #"""
						npm install -g genversion
						cd packages/cli
						genversion version.ts
						echo -n $(git rev-parse --abbrev-ref HEAD)									> /branch
						echo -n $(git rev-parse HEAD)            										> /sha
						echo -n $(git rev-parse --short=12 HEAD) 										> /shaTag
						echo -n $(cat version.ts | sed -n "s/^.*'\(.*\)'.*$/\1/ p") > /version
					"""#
				export: files: {
					"/branch":  string
					"/sha":     string
					"/shaTag":  string
					"/version": string
				}
			}
			branch:  run.export.files["/branch"]
			sha:     run.export.files["/sha"]
			shaTag:  run.export.files["/shaTag"]
			version: run.export.files["/version"]
		}

		push: [Region=aws.#Region]: [EnvTag=#EnvTag]: [Branch=#Branch]: [Sha=#Sha]: [ShaTag=#ShaTag]: [Version=string]: {
			_tags: ["\(EnvTag)", "\(Branch)", "\(Sha)", "\(ShaTag)"]
			_extraTags: [...string] | *[]
			if EnvTag == "prod" {
				_extraTags: ["latest", "\(Version)"]
			}
			if EnvTag == "tnet" {
				_extraTags: ["\(Version)"]
			}
			ecr: {
				if EnvTag == "dev" {
					qa_ceramic: utils.#ECR & {
						img: buildCeramic.output
						env: {
							AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
							AWS_ECR_SECRET: client.commands.aws.stdout
							AWS_REGION:     Region
							REPO:           "ceramic-prod"
							TAGS:           _tags + _extraTags + ["qa"]
						}
					}
					qa_cdb: utils.#ECR & {
						img: buildComposeDB.output
						env: {
							AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
							AWS_ECR_SECRET: client.commands.aws.stdout
							AWS_REGION:     Region
							REPO:           "composedb-prod"
							TAGS:           _tags + _extraTags + ["qa"]
						}
					}
				}
				ecr_ceramic: utils.#ECR & {
					img: buildCeramic.output
					env: {
						AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
						AWS_ECR_SECRET: client.commands.aws.stdout
						AWS_REGION:     Region
						REPO:           "ceramic-prod"
						TAGS:           _tags + _extraTags
					}
				}
				ecr_cdb: utils.#ECR & {
					img: buildComposeDB.output
					env: {
						AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
						AWS_ECR_SECRET: client.commands.aws.stdout
						AWS_REGION:     Region
						REPO:           "composedb-prod"
						TAGS:           _tags + _extraTags
					}
				}
			}
			dockerhub_ceramic: utils.#Dockerhub & {
				img: buildCeramic.output
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN:    client.env.DOCKERHUB_TOKEN
					REPO:               _repo_ceramic
					TAGS:               _tags + _extraTags
				}
			}
			dockerhub_cdb: utils.#Dockerhub & {
				img: buildComposeDB.output
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN:    client.env.DOCKERHUB_TOKEN
					REPO:               _repo_composeDB
					TAGS:               _tags + _extraTags
				}
			}
		}

		// Don't validate `Sha` and `ShaTag` - the CD manager will automatically pull valid values from the database.
		deploy: [Region=aws.#Region]: [EnvTag=#EnvTag]: [Sha=string]: [ShaTag=string]: {
			jobEnv: {
				AWS_ACCESS_KEY_ID:     client.env.AWS_ACCESS_KEY_ID
				AWS_SECRET_ACCESS_KEY: client.env.AWS_SECRET_ACCESS_KEY
				AWS_REGION:            Region
			}
			jobSpec: {
				type: "deploy"
				params: {
					component: "ceramic"
					sha:       Sha
					shaTag:    ShaTag
				}
			}
			_deployEnv: utils.#Job & {
				env: jobEnv & {
					ENV_TAG: "\(EnvTag)"
				}
				spec: jobSpec
			}
			if EnvTag == "dev" {
				_deployQa: utils.#Job & {
					env: jobEnv & {
						ENV_TAG: "qa"
					}
					spec: jobSpec
				}
			}
		}

		docs: utils.#Docs & {
			src: _fullSource
			run: env: NODE_OPTIONS: "--max_old_space_size=3584"
		}
	}
}
