package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"
)

#Push: {
	env: {
		AWS_ACCOUNT_ID:     string
		AWS_ECR_SECRET: 	dagger.#Secret
		AWS_REGION: 		aws.#Region
		DOCKERHUB_USERNAME: string
		DOCKERHUB_TOKEN: 	dagger.#Secret
	}

	params: {
		envTag:   *"dev" | "qa" | "tnet" | "prod"
		repo:     "js-ceramic" | "ceramic-anchor-service" | "go-ipfs-daemon"
		repoType: "ceramic" | "cas" | "ipfs"
		branch:   "dev" | "develop" | "release-candidate" | "tnet" | "main" | "master"
		sha:      string
		shaTag:   string
		image:	  docker.#Image
	}

	push: {
		for tag in [params.envTag, params.branch, params.sha, params.shaTag] {
			"ecr_\(tag)":  docker.#Push & {
				image: params.image
				dest:  "\(env.AWS_ACCOUNT_ID).dkr.ecr.\(env.AWS_REGION).amazonaws.com/ceramic-\(params.envTag):\(tag)"
				auth: {
					username: "AWS"
					secret: env.AWS_ECR_SECRET
				}
			}
			"dockerhub_\(tag)":  docker.#Push & {
				image: params.image
				dest:  "ceramicnetwork/js-ceramic:\(tag)"
				auth: {
					username: env.DOCKERHUB_USERNAME
					secret: env.DOCKERHUB_TOKEN
				}
			}
		}
	}
}
