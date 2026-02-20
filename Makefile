# Variables
PROFILE ?= default-etcd
GOOS ?= linux
GOARCH ?= amd64

SUPPORTED_PROFILES := default-etcd

ifeq ($(PROFILE),default-etcd)
TAGS := grpc etcd
else
$(error Unsupported PROFILE '$(PROFILE)'. Supported profiles: $(SUPPORTED_PROFILES))
endif

SANDSTORE_BINARY=sandstore
CLIENT_BINARY=bin/client
MCP_BINARY=sandstore-mcp
OPEN_SMOKE_BINARY=bin/open-smoke
LEGACY_CLIENT_BINARY=client
LEGACY_MCP_BINARY=mcp
LEGACY_OPEN_SMOKE_BINARY=open_smoke

# Generate protobuf files
.PHONY: proto
proto:
	PATH=$(PATH):$(shell go env GOPATH)/bin protoc --go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		proto/communication/communication.proto

# Build sandstore node
.PHONY: build
build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -tags="$(TAGS)" -o $(SANDSTORE_BINARY) ./cmd/sandstore

# Build MCP server
.PHONY: mcp
mcp:
	go build -o $(MCP_BINARY) ./clients/mcp

# Run simple server via cmd/sandstore
.PHONY: simple
simple:
	./scripts/dev/run-simple.sh

# Start 5-node Raft cluster
.PHONY: cluster
cluster:
	-./scripts/dev/run-5.sh

# Run the client
.PHONY: client
client:
	@mkdir -p $(dir $(CLIENT_BINARY))
	go build -o $(CLIENT_BINARY) ./clients/client
	./$(CLIENT_BINARY)

# Run Go tests
.PHONY: test
test:
	go test -v ./...

# Build Docker image
.PHONY: docker-build
docker-build:
	docker build --build-arg TAGS="$(TAGS)" --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(GOARCH) -t sandstore-node:latest -f deploy/docker/Dockerfile .

# Kill running sandstore processes
.PHONY: kill
kill:
	-@pgrep -fal sandstore || true
	-@pkill -f sandstore || true

# Clean up
.PHONY: clean
clean:
	rm -rf ./bin && docker compose -f deploy/docker/etcd/docker-compose.yaml down -v
