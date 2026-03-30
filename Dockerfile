# ─────────────────────────────────────────────────────────────────────────────
# notx-engine Dockerfile
#
# Multi-stage build:
#   Stage 1 (builder) — compiles the Go binary with a stub admin UI embed so
#                       the go:embed directive is satisfied without needing npm.
#   Stage 2 (runtime) — minimal Debian image containing only the binary.
#
# The resulting image exposes port 4060 (HTTP) and 50051 (gRPC).
#
# Build:
#   docker build -t notx:local .
#
# Run (local-mode, no passphrase):
#   docker run --rm -p 4060:4060 notx:local
#
# Run (remote-admin mode):
#   docker run --rm -p 4060:4060 notx:local server --admin-passphrase secret
# ─────────────────────────────────────────────────────────────────────────────

# ── Stage 1: builder ──────────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS builder

WORKDIR /src

# Copy dependency manifests first so Docker can cache the module download layer.
COPY go.mod go.sum ./
RUN go mod download

# The internal/admin package uses //go:embed ui, so a non-empty placeholder
# directory must exist before `go build` runs. We create a minimal index.html
# so the embed glob resolves to at least one file.
RUN mkdir -p internal/admin/ui && \
    echo '<!doctype html><html><body>notx admin (server-only build)</body></html>' \
    > internal/admin/ui/index.html

# Copy the full source tree (after the module cache layer).
COPY . .

# Rebuild the placeholder in case the COPY above overwrote it with nothing.
RUN mkdir -p internal/admin/ui && \
    echo '<!doctype html><html><body>notx admin (server-only build)</body></html>' \
    > internal/admin/ui/index.html

# Compile. CGO is disabled for a fully static binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-s -w \
        -X 'github.com/zebaqui/notx-engine/internal/buildinfo.Version=docker' \
        -X 'github.com/zebaqui/notx-engine/internal/buildinfo.Commit=unknown' \
        -X 'github.com/zebaqui/notx-engine/internal/buildinfo.BuildTime=unknown'" \
      -o /out/notx \
      ./cmd/notx

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS runtime

# ca-certificates is needed if the binary ever dials HTTPS upstreams.
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Create a non-root user for running the server, with a home directory so
# clientconfig can write ~/.notx/config.json without permission errors.
RUN useradd --system --create-home --shell /usr/sbin/nologin notx

# Data directory — mounted or ephemeral depending on the use-case.
RUN mkdir -p /data && chown notx:notx /data

COPY --from=builder /out/notx /usr/local/bin/notx

USER notx

# HTTP API
EXPOSE 4060
# gRPC (primary, mTLS)
EXPOSE 50051
# gRPC (bootstrap pairing listener, TLS only)
EXPOSE 50052

# Default: start the server with the file provider writing to /data.
# Override the entire CMD to pass flags such as --admin-passphrase.
ENTRYPOINT ["notx"]
CMD ["server", "--data-dir", "/data", "--grpc=false"]
