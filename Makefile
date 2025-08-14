# Variables
BASIC_SERVER_BINARY=basic-server
REPLICATED_SERVER_BINARY=replicated-server
RAFT_SERVER_BINARY=raft-server
CLIENT_BINARY=client
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
	go build -o $(BASIC_SERVER_BINARY) ./cmd/basic-server
	go build -o $(REPLICATED_SERVER_BINARY) ./cmd/replicated-server
	go build -o $(RAFT_SERVER_BINARY) ./cmd/raft-server
	go build -o $(CLIENT_BINARY) ./cmd/client

# Build MCP server
.PHONY: mcp
mcp:
	go build -o $(MCP_BINARY) ./cmd/mcp

# Run basic server (single node, no replication)
.PHONY: server-basic
server-basic:
	go build -o $(BASIC_SERVER_BINARY) ./cmd/basic-server
	./$(BASIC_SERVER_BINARY)

# Run replicated server (3-node cluster with replication)
.PHONY: server-replicated
server-replicated:
	go build -o $(REPLICATED_SERVER_BINARY) ./cmd/replicated-server
	./$(REPLICATED_SERVER_BINARY)

# Run Raft server (3-node cluster with Raft consensus)
.PHONY: server-raft
server-raft:
	go build -o $(RAFT_SERVER_BINARY) ./cmd/raft-server
	./$(RAFT_SERVER_BINARY)

# Backward compatibility - defaults to Raft server
.PHONY: server
server: server-raft

# Run the client
.PHONY: client
client:
	go build -o $(CLIENT_BINARY) ./cmd/client
	./$(CLIENT_BINARY)

# Test server-client communication with Raft
.PHONY: test-server
test-server:
	@echo "Starting Raft server..."
	@go build -o $(RAFT_SERVER_BINARY) ./cmd/raft-server
	@./$(RAFT_SERVER_BINARY) & SERVER_PID=$$!; \
	sleep 3; \
	echo "Running client..."; \
	go build -o $(CLIENT_BINARY) ./cmd/client && ./$(CLIENT_BINARY); \
	echo "Stopping server..."; \
	kill $$SERVER_PID

# Run Go tests
.PHONY: test
test:
	go test -v ./...

# Clean up
.PHONY: clean
clean:
	rm -f $(BASIC_SERVER_BINARY) $(REPLICATED_SERVER_BINARY) $(RAFT_SERVER_BINARY) $(CLIENT_BINARY) $(MCP_BINARY)
	rm -rf chunks logs