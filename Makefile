# ─────────────────────────────────────────────────────────────────────────────
# notx-engine Makefile
# ─────────────────────────────────────────────────────────────────────────────

ADMIN_DIR  := ui/admin
BINARY     := bin/notx
CMD        := ./cmd/notx

BINARY_CTL := bin/notxctl
CMD_CTL    := ./cmd/notxctl

PROTO_DIR  := internal/server/proto
PROTO_FILE := $(PROTO_DIR)/notx.proto

# Docker image tag used by the integration tests.
DOCKER_IMAGE := notx:integration-test

GOPATH     := $(shell go env GOPATH)
GOMODCACHE := $(shell go env GOMODCACHE)

PROTOC         := protoc
PROTOC_GEN_GO  := $(GOPATH)/bin/protoc-gen-go
PROTOC_GEN_GRP := $(GOPATH)/bin/protoc-gen-go-grpc

.PHONY: all build build-go build-ctl generate-proto clean \
        admin-install admin-dev admin-build \
        docker-build test-integration

# ── Default ───────────────────────────────────────────────────────────────────

all: build

# ── Full build (UI + Go binary) ───────────────────────────────────────────────

## build: build the admin UI then compile the notx binary (with UI embedded)
build:
	@bash scripts/build.sh

## build-skip-ui: recompile the Go binary without rebuilding the admin UI
build-skip-ui:
	@bash scripts/build.sh --skip-ui

## build-go: compile only the Go binary (no UI build, no embed staging)
##           useful for rapid iteration when the UI hasn't changed
build-go:
	@echo "  BUILD   $(BINARY)"
	@mkdir -p bin
	go build -o $(BINARY) $(CMD)

## build-ctl: compile the notxctl debug/ops CLI binary
build-ctl:
	@echo "  BUILD   $(BINARY_CTL)"
	@mkdir -p bin
	go build \
		-ldflags "-X github.com/zebaqui/notx-engine/internal/buildinfo.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) \
		          -X github.com/zebaqui/notx-engine/internal/buildinfo.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown) \
		          -X github.com/zebaqui/notx-engine/internal/buildinfo.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o $(BINARY_CTL) $(CMD_CTL)

# ── Proto ─────────────────────────────────────────────────────────────────────

## generate-proto: regenerate Go code from notx.proto
generate-proto:
	@echo "  PROTO   $(PROTO_FILE)"
	$(PROTOC) \
		--go_out=$(PROTO_DIR) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_DIR) \
		--go-grpc_opt=paths=source_relative \
		--proto_path=$(PROTO_DIR) \
		--proto_path=$(GOMODCACHE)/google.golang.org/protobuf@$(shell go list -m -f '{{.Version}}' google.golang.org/protobuf) \
		$(PROTO_FILE)

# ── Admin UI helpers ──────────────────────────────────────────────────────────

## admin-install: install npm dependencies for the admin UI
admin-install:
	@echo "  NPM     $(ADMIN_DIR)"
	cd $(ADMIN_DIR) && npm install

## admin-dev: start the admin UI dev server (proxies API calls to :4060)
admin-dev:
	@echo "  DEV     $(ADMIN_DIR)  →  http://localhost:5173"
	cd $(ADMIN_DIR) && npm run dev

## admin-build: build the admin UI for production (output: ui/admin/dist/)
admin-build:
	@echo "  BUILD   $(ADMIN_DIR)"
	cd $(ADMIN_DIR) && npm run build

# ── Docker ────────────────────────────────────────────────────────────────────

## docker-build: build the notx Docker image (server-only, no UI embed required)
docker-build:
	@echo "  DOCKER  $(DOCKER_IMAGE)"
	docker build --tag $(DOCKER_IMAGE) --file Dockerfile .

## test-integration: build the Docker image then run the ephemeral-container integration tests
##                   requires Docker; safe to skip in CI environments without Docker
test-integration: docker-build
	@echo "  TEST    ./tests/docker/ (integration)"
	go test -v -tags integration -timeout 120s ./tests/docker/

## test-pairing: build the Docker image then run only the server-pairing smoke tests
##               requires Docker; exercises Phases S1–S5 of the pairing design doc
test-pairing: docker-build
	@echo "  TEST    ./tests/docker/ -run TestServerPairing (integration)"
	go test -v -tags integration -timeout 180s ./tests/docker/ -run TestServerPairing

# ── Clean ─────────────────────────────────────────────────────────────────────

## clean: remove all build artifacts (binary, dist, embed staging dir)
clean:
	@echo "  CLEAN"
	@rm -f  $(BINARY)
	@rm -f  $(BINARY_CTL)
	@rm -rf $(ADMIN_DIR)/dist
	@rm -rf internal/admin/ui

# ── Help ──────────────────────────────────────────────────────────────────────

## help: print this help message
help:
	@echo ""
	@echo "  notx-engine build targets"
	@echo ""
	@grep -E '^## ' Makefile | sed 's/^## /  /' | column -t -s ':'
	@echo ""

.PHONY: help
