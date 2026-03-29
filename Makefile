# ─────────────────────────────────────────────────────────────────────────────
# notx-engine Makefile
# ─────────────────────────────────────────────────────────────────────────────

ADMIN_DIR  := ui/admin
BINARY     := bin/notx
CMD        := ./cmd/notx

PROTO_DIR  := internal/server/proto
PROTO_FILE := $(PROTO_DIR)/notx.proto

GOPATH     := $(shell go env GOPATH)
GOMODCACHE := $(shell go env GOMODCACHE)

PROTOC         := protoc
PROTOC_GEN_GO  := $(GOPATH)/bin/protoc-gen-go
PROTOC_GEN_GRP := $(GOPATH)/bin/protoc-gen-go-grpc

.PHONY: all build build-go generate-proto clean \
        admin-install admin-dev admin-build

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

# ── Clean ─────────────────────────────────────────────────────────────────────

## clean: remove all build artifacts (binary, dist, embed staging dir)
clean:
	@echo "  CLEAN"
	@rm -f  $(BINARY)
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
