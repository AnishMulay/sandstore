# Variables
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

# Build all binaries
.PHONY: build
build:
	go build -o $(SANDSTORE_BINARY) ./cmd/sandstore
	@mkdir -p $(dir $(CLIENT_BINARY))
	go build -o $(CLIENT_BINARY) ./cmd/client

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

# Kill running sandstore processes
.PHONY: kill
kill:
	-@pgrep -fal sandstore || true
	-@pkill -f sandstore || true

# Clean up
.PHONY: clean
clean:
	-@pgrep -fal sandstore || true
	-@pkill -f sandstore || true
	-@docker compose -f deploy/docker/etcd/docker-compose.yaml down --remove-orphans -v >/dev/null 2>&1 || true
	rm -f $(SANDSTORE_BINARY) $(CLIENT_BINARY) $(MCP_BINARY) $(OPEN_SMOKE_BINARY) $(LEGACY_CLIENT_BINARY) $(LEGACY_MCP_BINARY) $(LEGACY_OPEN_SMOKE_BINARY)
	rm -rf bin/ run/ chunks logs config.yaml
