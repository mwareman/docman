# syntax=docker/dockerfile:1

# DocMan builds to a single static binary with the UI embedded, so the runtime
# image is scratch plus that binary: no shell, no package manager, nothing to
# patch. The builder installs no packages either — the timezone database is
# compiled in via Go's time/tzdata, and the golang:alpine image already carries
# a CA bundle.

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev

ENV CGO_ENABLED=0 GOTOOLCHAIN=auto
WORKDIR /src

# Keep an untouched copy of the CA bundle for the runtime image, so a CA added
# below for build-time use only is not shipped to whoever runs DocMan.
RUN cp /etc/ssl/certs/ca-certificates.crt /tmp/ca-pristine.crt

# Networks that intercept TLS (corporate proxies such as Netskope or Zscaler)
# present their own certificate, which the build container will not trust. Drop
# the PEM for your root CA into build/certs/ and it is trusted while building.
COPY build/certs/ /usr/local/share/extra-ca/
RUN set -eux; \
    if ls /usr/local/share/extra-ca/*.crt >/dev/null 2>&1; then \
      cat /usr/local/share/extra-ca/*.crt >> /etc/ssl/certs/ca-certificates.crt; \
      echo "trusting extra CA certificates for this build"; \
    fi

COPY . .

# The only dependency is the pure-Go SQLite driver, pinned in go.mod and go.sum
# so every build uses the same versions. Download, not tidy: tidy also resolves
# the driver's test dependencies, whose newest releases can demand a newer Go.
RUN go mod download

RUN set -eux; \
    export GOOS="$TARGETOS" GOARCH="$TARGETARCH"; \
    case "$TARGETVARIANT" in v5) export GOARM=5 ;; v6) export GOARM=6 ;; v7) export GOARM=7 ;; esac; \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/docman . ; \
    mkdir -p /out/tmp && chmod 1777 /out/tmp


FROM scratch

LABEL org.opencontainers.image.title="DocMan" \
      org.opencontainers.image.description="Single-host Docker management with passkey sign-in" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/mwareman/docman" \
      org.opencontainers.image.authors="Michael Wareman"

COPY --from=build /tmp/ca-pristine.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/tmp /tmp
COPY --from=build /out/docman /docman

ENV DOCMAN_DATA_DIR=/data \
    DOCMAN_HTTPS_ADDR=:9444 \
    DOCMAN_HTTP_ADDR=:9080 \
    TZ=UTC

VOLUME ["/data"]
EXPOSE 9444 9080

ENTRYPOINT ["/docman"]
