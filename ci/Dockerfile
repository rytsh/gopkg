FROM golang:1.27-alpine AS go-runtime

FROM --platform=$BUILDPLATFORM alpine:latest AS turna

ARG TARGETARCH
ARG TURNA_VERSION=0.9.8

RUN apk add --no-cache ca-certificates curl
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) turna_arch=x86_64; turna_sha256=99907aab9c1b59edbdc7a8656a380a333f5d7462846682c5291af15244f90c91 ;; \
      arm64) turna_arch=arm64; turna_sha256=ebefc1faa059a4c5e6f7f86347e5ef43c9245ff9d8157093256809906df29372 ;; \
      *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fSL "https://github.com/rakunlabs/turna/releases/download/v${TURNA_VERSION}/turna_Linux_${turna_arch}.tar.gz" -o /tmp/turna.tar.gz; \
    echo "${turna_sha256}  /tmp/turna.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/turna.tar.gz -C /usr/local/bin turna

FROM --platform=$BUILDPLATFORM alpine:latest AS filesystem
RUN mkdir /proxy && \
    touch /etc/gopkg.yaml && \
    chown 65532:65532 /proxy /etc/gopkg.yaml && \
    chmod 0600 /etc/gopkg.yaml

FROM alpine:latest

ARG TARGETPLATFORM

COPY --from=go-runtime /usr/local/go /usr/local/go
COPY --from=filesystem --chown=65532:65532 /proxy /proxy
COPY --from=filesystem --chown=65532:65532 /etc/gopkg.yaml /etc/gopkg.yaml
COPY ${TARGETPLATFORM}/gopkg /usr/local/bin/gopkg
COPY --from=turna /usr/local/bin/turna /usr/local/bin/turna
COPY ci/turna.yaml /etc/turna.yaml

ENV PATH="/usr/local/go/bin:${PATH}" \
    HOME="/tmp" \
    GOCACHE="/tmp/go-build" \
    GOROOT="/usr/local/go" \
    GOTOOLCHAIN="local" \
    GOPROXY="off" \
    GOSUMDB="off" \
    GOVCS="*:off" \
    GOPKG_HTTP=":8080" \
    GOPKG_PROXY_DIR="/proxy" \
    GOPKG_FETCH_MISSING="false" \
    GOPKG_FETCH_TIMEOUT="2m" \
    GOPKG_REFRESH="30s"

USER 65532:65532
WORKDIR /proxy
VOLUME ["/proxy"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=2m \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/-/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/turna"]
