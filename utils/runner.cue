package utils

import (
    "universe.dagger.io/alpine"
    "universe.dagger.io/docker"
)

#Runner: {
	ver: string | *"0.2.20"
	docker.#Build & {
		steps: [
			alpine.#Build & {
				// Only install packages needed later
				packages: {
					bash: 		  {}
					curl: 		  {}
					"docker-cli": {}
					jq: 		  {}
				}
			},
			// Install AWS CLI v2
			// Ref: https://stackoverflow.com/questions/60298619/awscli-version-2-on-alpine-linux
			docker.#Run & {
				env: {
					AWSCLI_VERSION: string | *"2.7.5"
					GLIBC_VERSION:  string | *"2.34-r0"
				}
				command: {
					name: "sh"
					flags: "-c": #"""
						apk --no-cache add \
							binutils \
							unzip \
						&& curl -sL https://alpine-pkgs.sgerrand.com/sgerrand.rsa.pub -o /etc/apk/keys/sgerrand.rsa.pub \
						&& curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/${GLIBC_VERSION}/glibc-${GLIBC_VERSION}.apk \
						&& curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/${GLIBC_VERSION}/glibc-bin-${GLIBC_VERSION}.apk \
						&& curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/${GLIBC_VERSION}/glibc-i18n-${GLIBC_VERSION}.apk \
						&& apk add --no-cache \
							glibc-${GLIBC_VERSION}.apk \
							glibc-bin-${GLIBC_VERSION}.apk \
							glibc-i18n-${GLIBC_VERSION}.apk \
						&& /usr/glibc-compat/bin/localedef -i en_US -f UTF-8 en_US.UTF-8 \
						&& curl -sL https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip -o awscliv2.zip \
						&& unzip -q awscliv2.zip \
						&& aws/install \
						&& rm -rf \
							awscliv2.zip \
							aws \
							/usr/local/aws-cli/v2/current/dist/aws_completer \
							/usr/local/aws-cli/v2/current/dist/awscli/data/ac.index \
							/usr/local/aws-cli/v2/current/dist/awscli/examples \
							glibc-*.apk \
						&& find /usr/local/aws-cli/v2/current/dist/awscli/botocore/data -name examples-1.json -delete \
						&& apk --no-cache del \
							binutils \
							unzip \
						&& rm -rf /var/cache/apk/*
						aws --version
					"""#
				}
			},
			// Install Dagger
			docker.#Run & {
				env: DAGGER_VERSION: "\(ver)"
				command: {
					name: "sh"
					flags: "-c": #"""
						curl -fsSL https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=$DAGGER_VERSION sh
						dagger version
					"""#
				}
			},
		]
	}
}
