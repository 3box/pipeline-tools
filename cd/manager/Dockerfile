FROM golang:1.18.3-buster as builder

# Install deps
RUN apt-get update && apt-get install -y \
  libssl-dev \
  ca-certificates \
  fuse

ENV SRC_DIR /cd-manager

# Download packages first so they can be cached.
COPY go.mod go.sum $SRC_DIR/
COPY common/aws/utils/go.mod common/aws/utils/go.sum $SRC_DIR/common/aws/utils/
COPY common/job/go.mod common/job/go.sum $SRC_DIR/common/job/
RUN cd $SRC_DIR && go mod download

COPY . $SRC_DIR

RUN cd $SRC_DIR/cmd/manager \
  && CGO_ENABLED=0 GOOS=linux GOTAGS=openssl go build -a -o cd-manager .

# Get tini, a very minimal init daemon for containers.
ENV TINI_VERSION v0.19.0
RUN set -eux; \
    dpkgArch="$(dpkg --print-architecture)"; \
    case "${dpkgArch##*-}" in \
        "amd64" | "armhf" | "arm64") tiniArch="tini-static-$dpkgArch" ;;\
        *) echo >&2 "unsupported architecture: ${dpkgArch}"; exit 1 ;; \
    esac; \
  cd /tmp \
  && wget -q -O tini https://github.com/krallin/tini/releases/download/$TINI_VERSION/$tiniArch \
  && chmod +x tini

# Now comes the actual target image, which aims to be as small as possible.
FROM busybox:1.31.1-glibc as target

ARG ENV_TAG
ENV ENV_TAG=$ENV_TAG

COPY --from=builder /tmp/tini /sbin/tini
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

# This shared lib (part of glibc) doesn't seem to be included with busybox.
COPY --from=builder /lib/*-linux-gnu*/libdl.so.2 /lib/

# Copy over SSL libraries.
COPY --from=builder /usr/lib/*-linux-gnu*/libssl.so* /usr/lib/
COPY --from=builder /usr/lib/*-linux-gnu*/libcrypto.so* /usr/lib/

# Get the cd-manager binary and environment variables from the build container.
ENV SRC_DIR /cd-manager
COPY --from=builder $SRC_DIR/env/.env."$ENV_TAG" /usr/local/bin/manager/env/.env
COPY --from=builder $SRC_DIR/cmd/manager/cd-manager /usr/local/bin/manager/cd-manager

EXPOSE 8080

WORKDIR /usr/local/bin/manager

CMD ["/sbin/tini", "-s", "./cd-manager"]
