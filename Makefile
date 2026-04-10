ADMIN_DIR  := ui/admin
BINARY     := bin/notx
BINARY_CTL := bin/notxctl
CMD        := ./cmd/notx
CMD_CTL    := ./cmd/notxctl
PROTO_DIR  := proto
PROTO_FILES := \
	$(PROTO_DIR)/note.proto \
	$(PROTO_DIR)/device.proto \
	$(PROTO_DIR)/project.proto \
	$(PROTO_DIR)/folder.proto \
	$(PROTO_DIR)/user.proto \
	$(PROTO_DIR)/server.proto \
	$(PROTO_DIR)/relay.proto \
	$(PROTO_DIR)/context.proto \
	$(PROTO_DIR)/link.proto

DOCKER_IMAGE := notx:integration-test

GOPATH            := $(shell go env GOPATH)
PROTOC            := protoc
PROTOC_GEN_GO     := $(GOPATH)/bin/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(GOPATH)/bin/protoc-gen-go-grpc
PROTOC_INCLUDE    := $(dir $(shell which $(PROTOC)))../include

MOBILE_PKG        := github.com/zebaqui/notx-engine/mobile
MOBILE_OUT        := Notx.xcframework
GOMOBILE          := $(GOPATH)/bin/gomobile

.PHONY: all build build-skip-ui build-go build-ctl \
        install-proto-plugins generate-proto \
        admin-install admin-dev admin-build \
        mobile mobile-install \
        docker-build docker-authority-server docker-authority-server-stop docker-authority-server-volume docker-authority-server-persist test-smoke test-integration test-pairing \
        clean

all: build

build:
	@bash scripts/build.sh

build-skip-ui:
	@bash scripts/build.sh --skip-ui

build-go:
	@mkdir -p bin
	go build -o $(BINARY) $(CMD)

build-ctl:
	@mkdir -p bin
	go build -o $(BINARY_CTL) $(CMD_CTL)

install-proto-plugins:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

generate-proto: $(PROTOC_GEN_GO) $(PROTOC_GEN_GO_GRPC)
	$(PROTOC) \
		--go_out=$(PROTO_DIR) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_DIR) \
		--go-grpc_opt=paths=source_relative \
		--proto_path=$(PROTO_DIR) \
		--proto_path=$(PROTOC_INCLUDE) \
		$(PROTO_FILES)

admin-install:
	cd $(ADMIN_DIR) && npm install

admin-dev:
	cd $(ADMIN_DIR) && npm run dev

admin-build:
	cd $(ADMIN_DIR) && npm run build

# ── Mobile (gomobile bind → Notx.xcframework) ────────────────────────────────

## Install gomobile and gobind into GOPATH/bin.
mobile-install:
	go install golang.org/x/mobile/cmd/gomobile@latest
	go install golang.org/x/mobile/cmd/gobind@latest
	$(GOMOBILE) init

## Build Notx.xcframework for iOS (device + simulator).
## Output: ./Notx.xcframework  — drag into Xcode, set Embed & Sign.
## GOFLAGS=-mod=mod is required because the project uses a vendor/ directory
## but golang.org/x/mobile is not vendored — gomobile must use the module cache.
mobile:
	GOFLAGS=-mod=mod GOWORK=off $(GOMOBILE) bind \
		-target ios \
		-o $(MOBILE_OUT) \
		$(MOBILE_PKG)
	@echo "✓ $(MOBILE_OUT) ready — add to Xcode via General → Frameworks, Libraries, and Embedded Content"

docker-build:
	docker build --tag $(DOCKER_IMAGE) --file Dockerfile .

# ── Authority Server Recipe ────────────────────────────────────────────────────
# Runs the notx server as an authority instance in a Docker container.
# Uses non-standard ports so an authority and a regular server can coexist
# on the same host without conflicts.
# Exposes:
#   - HTTP API:             127.0.0.1:4070 (notx.local:4070)
#   - Primary gRPC (mTLS):  127.0.0.1:50060 (notx.local:50060)
#   - Bootstrap gRPC (TLS): 127.0.0.1:50061 (notx.local:50061)
#
# Prerequisites:
#   - Add "127.0.0.1 notx.local" to /etc/hosts
#
# Usage:
#   make docker-authority-server                     # start authority server
#   make docker-authority-server ADMIN_PASS="secret" # with admin passphrase
#
AUTHORITY_HTTP_PORT      := 4070
AUTHORITY_GRPC_PORT      := 50060
AUTHORITY_BOOTSTRAP_PORT := 50061

docker-authority-server: docker-build
	docker run --rm \
		-p 127.0.0.1:$(AUTHORITY_HTTP_PORT):$(AUTHORITY_HTTP_PORT) \
		-p 127.0.0.1:$(AUTHORITY_GRPC_PORT):$(AUTHORITY_GRPC_PORT) \
		-p 127.0.0.1:$(AUTHORITY_BOOTSTRAP_PORT):$(AUTHORITY_BOOTSTRAP_PORT) \
		--name notx-authority \
		--hostname notx.local \
		$(DOCKER_IMAGE) \
		server --pairing --device-auto-approve --host 0.0.0.0 --foreground \
			--http-port $(AUTHORITY_HTTP_PORT) \
			--grpc-port $(AUTHORITY_GRPC_PORT) \
			--pairing-port $(AUTHORITY_BOOTSTRAP_PORT) \
			$(if $(ADMIN_PASS),--admin-passphrase $(ADMIN_PASS),)

docker-authority-server-stop:
	docker stop notx-authority || true

docker-authority-server-volume:
	docker volume create notx-authority-data || true
	@echo "✓ Created docker volume 'notx-authority-data' for persistent storage"

docker-authority-server-persist: docker-build docker-authority-server-volume
	docker run --rm -d \
		-v notx-authority-data:/data \
		-p 127.0.0.1:$(AUTHORITY_HTTP_PORT):$(AUTHORITY_HTTP_PORT) \
		-p 127.0.0.1:$(AUTHORITY_GRPC_PORT):$(AUTHORITY_GRPC_PORT) \
		-p 127.0.0.1:$(AUTHORITY_BOOTSTRAP_PORT):$(AUTHORITY_BOOTSTRAP_PORT) \
		--name notx-authority \
		--hostname notx.local \
		$(DOCKER_IMAGE) \
		server --pairing --device-auto-approve --data-dir /data --host 0.0.0.0 --foreground \
			--http-port $(AUTHORITY_HTTP_PORT) \
			--grpc-port $(AUTHORITY_GRPC_PORT) \
			--pairing-port $(AUTHORITY_BOOTSTRAP_PORT) \
			$(if $(ADMIN_PASS),--admin-passphrase $(ADMIN_PASS),)
	@echo "✓ Authority server started in background (persistent mode)"
	@echo "  HTTP API:             http://notx.local:$(AUTHORITY_HTTP_PORT)"
	@echo "  gRPC:                 notx.local:$(AUTHORITY_GRPC_PORT)"
	@echo "  Bootstrap (pairing):  notx.local:$(AUTHORITY_BOOTSTRAP_PORT)"
	@echo "  Stop with: make docker-authority-server-stop"
	@echo "  View logs with: docker logs -f notx-authority"

test-smoke:
	go test -v -timeout 60s ./tests/smoke/

test-integration: docker-build
	go test -v -tags integration -timeout 120s ./tests/docker/

test-pairing: docker-build
	go test -v -tags integration -timeout 180s ./tests/docker/ -run TestServerPairing

clean:
	@rm -f  $(BINARY) $(BINARY_CTL)
	@rm -rf $(ADMIN_DIR)/dist internal/admin/ui
	@rm -rf $(MOBILE_OUT)
