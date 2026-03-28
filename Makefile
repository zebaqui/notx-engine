# ─────────────────────────────────────────────────────────────────────────────
# notx-engine Makefile
# ─────────────────────────────────────────────────────────────────────────────

BINARY     := bin/notx
CMD        := ./cmd/notx

PROTO_DIR  := internal/server/proto
PROTO_FILE := $(PROTO_DIR)/notx.proto

GOPATH     := $(shell go env GOPATH)
GOMODCACHE := $(shell go env GOMODCACHE)

PROTOC         := protoc
PROTOC_GEN_GO  := $(GOPATH)/bin/protoc-gen-go
PROTOC_GEN_GRP := $(GOPATH)/bin/protoc-gen-go-grpc

.PHONY: all build generate-proto clean

all: build

## build: compile the notx binary into bin/
build:
	@echo "  BUILD   $(BINARY)"
	@mkdir -p bin
	go build -o $(BINARY) $(CMD)

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

## clean: remove build artifacts
clean:
	@echo "  CLEAN"
	@rm -f $(BINARY)
