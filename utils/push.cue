package utils

import (
	"dagger.io/dagger"

	"universe.dagger.io/aws"
	"universe.dagger.io/docker"
)

#ECR: {
	repo: string

	env: {
		AWS_ACCOUNT_ID:     string
		AWS_ECR_SECRET: 	dagger.#Secret
		AWS_REGION: 		aws.#Region
	}

	params: {
		envTag:   #EnvTag
		repo:     #Repo
		branch:   #Branch
		sha:      string
		shaTag:   string
		image:	  docker.#Image
	}

	push: {
		for tag in [params.envTag, params.branch, params.sha, params.shaTag] {
			"ecr_\(tag)": docker.#Push & {
				image: params.image
				dest:  "\(env.AWS_ACCOUNT_ID).dkr.ecr.\(env.AWS_REGION).amazonaws.com/\(repo):\(tag)"
				auth: {
					username: "AWS"
					secret: env.AWS_ECR_SECRET
				}
			}
		}
	}
}

#Dockerhub: {
	env: {
		DOCKERHUB_USERNAME: string
		DOCKERHUB_TOKEN: 	dagger.#Secret
	}

	params: {
		envTag:   #EnvTag
		repo:     #Repo
		branch:   #Branch
		sha:      string
		shaTag:   string
		image:	  docker.#Image
	}

	push: {
		for tag in [params.envTag, params.branch, params.sha, params.shaTag] {
			"dockerhub_\(tag)": docker.#Push & {
				image: params.image
				dest:  "ceramicnetwork/\(params.repo):\(tag)"
				auth: {
					username: env.DOCKERHUB_USERNAME
					secret: env.DOCKERHUB_TOKEN
				}
			}
		}
	}
}
