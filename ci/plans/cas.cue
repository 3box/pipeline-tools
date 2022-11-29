package plans

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/aws"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"

	"github.com/3box/pipeline-tools/ci/utils"
)

#Branch: "develop" | "release-candidate" | "main"
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
	client: filesystem: source: read: {
		path:     "."
		contents: dagger.#FS
		exclude: [
			".github",
			"cue.mod",
			"node_modules",
			"build",
		]
	}
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		_repo:       "ceramic-anchor-service"
		_source:     client.filesystem.source.read.contents
		_dockerHost: client.network."unix:///var/run/docker.sock".connect

		test: utils.#TestNode & {
			src: _source
		}

		_baseImage: docker.#Dockerfile & {
			target: "base"
			source: _source
		}

		_runnerImage: docker.#Dockerfile & {
			target: "runner"
			source: _source
		}

		verify: {
			_endpoint:  "api/v0/healthcheck"
			_port:      8081
			_imageName: "ci-test-image"
			_loadImage: cli.#Load & {
				image: _baseImage.output
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
				}
				input:   _cli.output
				workdir: "/src"
				mounts: source: {
					dest:     "/src"
					contents: _source
				}
				mounts: docker: {
					contents: _dockerHost
					dest:     "/var/run/docker.sock"
				}
				always: true
				script: contents: #"""
						docker rm -f "$IMAGE_NAME" pg
						docker network create ci-test || true
						docker run -d --name pg \
							--network ci-test \
							-p 5432:5432 \
							-e POSTGRES_PASSWORD=root \
							-e POSTGRES_USER=root \
							-e POSTGRES_DB=anchor_db \
							postgres
						docker run -d --name "$IMAGE_NAME" \
							--network ci-test \
							-p 8081:8081 \
							-e NODE_ENV=dev \
							-e APP_MODE=server \
							-e APP_PORT=8081 \
							-e DB_NAME=anchor_db \
							-e DB_HOST=pg \
							-e DB_USERNAME=root \
							-e DB_PASSWORD=root \
							-e BLOCKCHAIN_CONNECTOR=ethereum \
							-e ETH_GAS_LIMIT=4712388 \
							-e ETH_GAS_PRICE=100000000000 \
							-e ETH_NETWORK=goerli \
							-e ETH_OVERRIDE_GAS_CONFIG=false \
							-e ETH_WALLET_PK=0x16dd0990d19001c50eeea6d32e8fdeef40d3945962caf18c18c3930baa5a6ec9 \
							"$IMAGE_NAME"

						timeout=$TIMEOUT
						until [[ $timeout -le 0 ]]; do
							echo -e "\n=============== Startup Logs ===============\n"
							docker logs --details --timestamps --tail 100 "$IMAGE_NAME"
							curl --verbose --fail --connect-timeout 5 --location "$URL" > curl.out 2>&1 || true

							if grep -q "200 OK" curl.out
							then
								echo Healthcheck passed
								docker rm -f "$IMAGE_NAME" pg
								docker network rm ci-test
								exit 0
							fi

							sleep 1
							timeout=$(( timeout - 1 ))
						done

						if [ $timeout -le 0 ]; then
							echo Healthcheck failed
							docker rm -f "$IMAGE_NAME" pg
							docker network rm ci-test
							exit 1
						fi
					"""#
			}
		}

		push: [Region=aws.#Region]: [EnvTag=#EnvTag]: [Branch=#Branch]: [Sha=#Sha]: [ShaTag=#ShaTag]: {
			_baseTags: ["\(EnvTag)", "\(Branch)", "\(Sha)", "\(ShaTag)"]
			_tags: [...string]
			{
				Branch == "main"
				_tags: _baseTags + ["latest"]
			} | {
				_tags: _baseTags
			}
			ecr: {
				if EnvTag == "dev" {
					_qa: {
						_base: utils.#ECR & {
							img: _baseImage.output
							env: {
								AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
								AWS_ECR_SECRET: client.commands.aws.stdout
								AWS_REGION:     Region
								REPO:           "ceramic-qa-cas"
								TAGS:           _tags + ["qa"]
							}
						}
						_runner: utils.#ECR & {
							img: _runnerImage.output
							env: {
								AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
								AWS_ECR_SECRET: client.commands.aws.stdout
								AWS_REGION:     Region
								REPO:           "ceramic-qa-cas-runner"
								TAGS:           _tags + ["qa"]
							}
						}
					}
				}
				_base: utils.#ECR & {
					img: _baseImage.output
					env: {
						AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
						AWS_ECR_SECRET: client.commands.aws.stdout
						AWS_REGION:     Region
						REPO:           "ceramic-\(EnvTag)-cas"
						TAGS:           _tags
					}
				}
				_runner: utils.#ECR & {
					img: _runnerImage.output
					env: {
						AWS_ACCOUNT_ID: client.env.AWS_ACCOUNT_ID
						AWS_ECR_SECRET: client.commands.aws.stdout
						AWS_REGION:     Region
						REPO:           "ceramic-\(EnvTag)-cas-runner"
						TAGS:           _tags
					}
				}
			}
			dockerhub: utils.#Dockerhub & {
				img: _baseImage.output
				env: {
					DOCKERHUB_USERNAME: client.env.DOCKERHUB_USERNAME
					DOCKERHUB_TOKEN:    client.env.DOCKERHUB_TOKEN
					REPO:               _repo
					TAGS:               _tags
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
			jobParams: {
				type: "deploy"
				params: {
					component: "cas"
					sha:       Sha
					shaTag:    ShaTag
				}
			}
			_deployEnv: utils.#Job & {
				env: jobEnv & {
					ENV_TAG: "\(EnvTag)"
				}
				job: jobParams
			}
			if EnvTag == "dev" {
				_deployQa: utils.#Job & {
					env: jobEnv & {
						ENV_TAG: "qa"
					}
					job: jobParams
				}
			}
		}
	}
}
