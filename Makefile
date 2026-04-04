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
	$(PROTO_DIR)/relay.proto

DOCKER_IMAGE := notx:integration-test

GOPATH            := $(shell go env GOPATH)
PROTOC            := protoc
PROTOC_GEN_GO     := $(GOPATH)/bin/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(GOPATH)/bin/protoc-gen-go-grpc
PROTOC_INCLUDE    := $(dir $(shell which $(PROTOC)))../include

.PHONY: all build build-skip-ui build-go build-ctl \
        install-proto-plugins generate-proto \
        admin-install admin-dev admin-build \
        docker-build test-smoke test-integration test-pairing \
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

docker-build:
	docker build --tag $(DOCKER_IMAGE) --file Dockerfile .

test-smoke:
	go test -v -timeout 60s ./tests/smoke/

test-integration: docker-build
	go test -v -tags integration -timeout 120s ./tests/docker/

test-pairing: docker-build
	go test -v -tags integration -timeout 180s ./tests/docker/ -run TestServerPairing

clean:
	@rm -f  $(BINARY) $(BINARY_CTL)
	@rm -rf $(ADMIN_DIR)/dist internal/admin/ui
