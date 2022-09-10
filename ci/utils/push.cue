package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"
)

#ECR: {
	img: docker.#Image

	env: {
		AWS_ACCOUNT_ID: string
		AWS_ECR_SECRET: dagger.#Secret
		AWS_REGION:     aws.#Region
		REPO:           string
		TAGS: [...string]
	}

	push: {
		for tag in env.TAGS {
			"ecr_\(tag)": docker.#Push & {
				image: img
				dest:  "\(env.AWS_ACCOUNT_ID).dkr.ecr.\(env.AWS_REGION).amazonaws.com/\(env.REPO):\(tag)"
				auth: {
					username: "AWS"
					secret:   env.AWS_ECR_SECRET
				}
			}
		}
	}
}

#Dockerhub: {
	img: docker.#Image

	env: {
		DOCKERHUB_USERNAME: string
		DOCKERHUB_TOKEN:    dagger.#Secret
		REPO:               string
		TAGS: [...string]
	}

	push: {
		for tag in env.TAGS {
			"dockerhub_\(tag)": docker.#Push & {
				image: img
				dest:  "ceramicnetwork/\(env.REPO):\(tag)"
				auth: {
					username: env.DOCKERHUB_USERNAME
					secret:   env.DOCKERHUB_TOKEN
				}
			}
		}
	}
}
