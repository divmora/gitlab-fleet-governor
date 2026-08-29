# ==============================================================================
# Build Stage
# ==============================================================================
ARG GO_VERSION=1.26-alpine
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder

# Build arguments for build metadata injection
ARG VERSION=dev
ARG GIT_COMMIT=none
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH

# Install build dependencies, CA certificates, and timezone data
RUN apk update && apk add --no-cache \
    git \
    ca-certificates \
    tzdata \
    make \
    && update-ca-certificates

# Create non-root system user and group (UID/GID 10001)
RUN addgroup -g 10001 -S appgroup && \
    adduser -u 10001 -S -G appgroup -h /home/appuser -s /sbin/nologin appuser

WORKDIR /src

# Leverage Docker layer caching for dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

# Copy source tree
COPY . .

# Compile static binary with optimizations and metadata injection
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath \
    -ldflags="-s -w \
      -X github.com/divmora/gitlab-fleet-governor/pkg/version.Version=${VERSION} \
      -X github.com/divmora/gitlab-fleet-governor/pkg/version.GitCommit=${GIT_COMMIT} \
      -X github.com/divmora/gitlab-fleet-governor/pkg/version.BuildDate=${BUILD_DATE}" \
    -o /build/gitlab-fleet-governor ./cmd/gitlab-fleet-governor

# ==============================================================================
# Final Runtime Stage
# ==============================================================================
FROM alpine:3.21 AS final

# Install runtime dependencies (certificates, timezone data)
RUN apk update && apk add --no-cache \
    ca-certificates \
    tzdata \
    && rm -rf /var/cache/apk/*

# Copy user/group definitions
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Copy compiled binary
COPY --from=builder --chown=10001:10001 /build/gitlab-fleet-governor /usr/local/bin/gitlab-fleet-governor

# Create workspace and configuration directories with non-root ownership
RUN mkdir -p /home/appuser /app /config && \
    chown -R 10001:10001 /home/appuser /app /config

# OCI standard image annotations
LABEL org.opencontainers.image.title="gitlab-fleet-governor" \
      org.opencontainers.image.description="Declarative policy-as-code and governance automation engine for GitLab fleets" \
      org.opencontainers.image.url="https://github.com/divmora/gitlab-fleet-governor" \
      org.opencontainers.image.source="https://github.com/divmora/gitlab-fleet-governor" \
      org.opencontainers.image.vendor="divmora" \
      org.opencontainers.image.licenses="Apache-2.0"

ENV USER=appuser \
    HOME=/home/appuser \
    PATH="/usr/local/bin:${PATH}" \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

USER 10001:10001
WORKDIR /app

ENTRYPOINT ["/usr/local/bin/gitlab-fleet-governor"]
CMD ["--help"]
