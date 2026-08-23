# Build stage
FROM golang:1.27-alpine AS builder

WORKDIR /app

# This project uses modernc.org/sqlite, a pure-Go SQLite. No cgo toolchain and no
# sqlite-dev headers are needed — the previous CGO_ENABLED=1 build pulled in gcc,
# musl-dev and sqlite-dev for nothing and produced a dynamically linked binary.

# Copy module definition first for layer caching. go.sum is committed, so
# `go mod download` is reproducible and `go mod tidy` has no business running
# during a build.
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . ./

# Build the headless API server (webui is embedded via go:embed)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o osante-proxy ./cmd/server

# Runtime stage
FROM alpine:latest

# Install runtime dependencies (no sqlite-libs: the binary is static)
RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app

# Prepare data directory
RUN mkdir -p /data

# Copy binary from builder
COPY --from=builder /app/osante-proxy /app/osante-proxy

# Environment variables
ENV OSANTE_DATA_DIR=/data
ENV OSANTE_PORT=12710
ENV OSANTE_DB_PATH=/data/osante.db
# The default bind is 127.0.0.1, which inside a container makes the published
# port unreachable from the host. A container is its own network namespace, so
# binding all interfaces here is what "loopback only" means on the host side —
# do not publish the port to a public interface.
ENV OSANTE_BIND=0.0.0.0

# Expose HTTP API port
EXPOSE 12710

# Health check targets the API health endpoint
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:12710/health || exit 1

# Volume for persistent data
VOLUME ["/data"]

# Run the headless server
ENTRYPOINT ["/app/osante-proxy"]
