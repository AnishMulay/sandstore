# Variables
SANDSTORE_BINARY=sandstore
CLIENT_BINARY=bin/client
MCP_BINARY=sandstore-mcp

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

# Clean up
.PHONY: clean
clean:
	-@pgrep -fal sandstore || true
	-@pkill -f sandstore || true
	rm -f $(SANDSTORE_BINARY) $(CLIENT_BINARY) $(MCP_BINARY)
	rm -rf bin/ run/ chunks logs config.yaml
