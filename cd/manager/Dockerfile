FROM golang:1.18.3-buster

# Install deps
RUN apt-get update && apt-get install -y \
  libssl-dev \
  ca-certificates \
  fuse

ENV SRC_DIR /cd-manager

# Download packages first so they can be cached.
COPY go.mod go.sum $SRC_DIR/
RUN cd $SRC_DIR \
  && go mod download

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
FROM busybox:1.31.1-glibc

# Get the cd-manager binary from the build container.
ENV SRC_DIR /cd-manager
COPY --from=0 $SRC_DIR/cmd/manager/cd-manager /usr/local/bin/manager/cd-manager
COPY --from=0 $SRC_DIR/env/* /usr/local/bin/manager/env/
COPY --from=0 /tmp/tini /sbin/tini
COPY --from=0 /etc/ssl/certs /etc/ssl/certs

# This shared lib (part of glibc) doesn't seem to be included with busybox.
COPY --from=0 /lib/*-linux-gnu*/libdl.so.2 /lib/

# Copy over SSL libraries.
COPY --from=0 /usr/lib/*-linux-gnu*/libssl.so* /usr/lib/
COPY --from=0 /usr/lib/*-linux-gnu*/libcrypto.so* /usr/lib/

ENV ENV_TAG=$ENV_TAG

EXPOSE 8080

WORKDIR /usr/local/bin/manager

CMD ["/sbin/tini", "-s", "./cd-manager"]
