FROM golang:1.27-alpine AS go-runtime

FROM --platform=$BUILDPLATFORM alpine:latest AS filesystem
RUN mkdir /proxy && chown 65532:65532 /proxy

FROM alpine:latest

ARG TARGETPLATFORM

COPY --from=go-runtime /usr/local/go /usr/local/go
COPY --from=filesystem --chown=65532:65532 /proxy /proxy
COPY ${TARGETPLATFORM}/gopkg /usr/local/bin/gopkg

ENV PATH="/usr/local/go/bin:${PATH}" \
    HOME="/tmp" \
    GOCACHE="/tmp/go-build" \
    GOROOT="/usr/local/go" \
    GOTOOLCHAIN="local" \
    GOPROXY="off" \
    GOSUMDB="off" \
    GOVCS="*:off"

USER 65532:65532
WORKDIR /proxy
VOLUME ["/proxy"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=2m \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/-/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/gopkg"]
CMD ["-http", ":8080", "-proxy-dir", "/proxy"]
