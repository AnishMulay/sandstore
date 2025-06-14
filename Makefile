# Variables
SERVER_BINARY=server
CLIENT_BINARY=client
SERVER_PATH=./cmd/server
CLIENT_PATH=./cmd/client
GRPC_PORT=8081

# Default target
.PHONY: all
all: build

# Build both server and client
.PHONY: build
build: build-server build-client

# Build the server
.PHONY: build-server
build-server:
	go build -o $(SERVER_BINARY) $(SERVER_PATH)

# Build the client
.PHONY: build-client
build-client:
	go build -o $(CLIENT_BINARY) $(CLIENT_PATH)

# Run the server
.PHONY: run-server
run-server: build-server
	./$(SERVER_BINARY)

# Run the client
.PHONY: run-client
run-client: build-client
	./$(CLIENT_BINARY)

# Run gRPC server
.PHONY: run-grpc-server
run-grpc-server: build-server
	./$(SERVER_BINARY) -grpc -addr=:$(GRPC_PORT)

# Run gRPC client
.PHONY: run-grpc-client
run-grpc-client: build-client
	./$(CLIENT_BINARY) -grpc -server=localhost:$(GRPC_PORT)

# Run both server and client for testing
.PHONY: test-run
test-run:
	@echo "Starting server in background..."
	@./$(SERVER_BINARY) & SERVER_PID=$$!; \
	echo "Waiting for server to start..."; \
	sleep 1; \
	echo "Starting client..."; \
	./$(CLIENT_BINARY); \
	echo "Stopping server..."; \
	kill $$SERVER_PID

# Clean up binary files
.PHONY: clean
clean:
	go clean
	rm -f $(SERVER_BINARY) $(CLIENT_BINARY)

# Test the application
.PHONY: test
test:
	go test ./...