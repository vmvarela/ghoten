# Ghoten Dockerfile
# Fork-specific Dockerfile for building Ghoten container images
#
# Build: docker build -t ghoten -f .github/docker/Dockerfile .
# Run:   docker run --rm ghoten version

FROM golang:1.23-alpine AS builder

WORKDIR /src

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/opentofu/opentofu/version.dev=no" \
    -o /ghoten ./cmd/tofu

# Final image
FROM alpine:3.19

LABEL maintainer="Victor M. Varela"
LABEL org.opencontainers.image.title="Ghoten"
LABEL org.opencontainers.image.description="OpenTofu fork with ORAS backend for OCI registry state storage"
LABEL org.opencontainers.image.source="https://github.com/vmvarela/ghoten"
LABEL org.opencontainers.image.licenses="MPL-2.0"

# Install runtime dependencies
RUN apk add --no-cache ca-certificates git openssh-client

# Copy binary from builder
COPY --from=builder /ghoten /usr/local/bin/ghoten

# Create non-root user
RUN adduser -D -u 1000 ghoten
USER ghoten

WORKDIR /workspace

ENTRYPOINT ["ghoten"]
CMD ["--help"]
