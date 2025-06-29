# Variables
SERVER_BINARY=server
CLIENT_BINARY=client

# Build both server and client
.PHONY: build
build:
	go build -o $(SERVER_BINARY) ./cmd/server
	go build -o $(CLIENT_BINARY) ./cmd/client

# Run the server
.PHONY: server
server: build
	./$(SERVER_BINARY)

# Run the client
.PHONY: client
client: build
	./$(CLIENT_BINARY)

# Test server-client communication
.PHONY: test-server
test-server: build
	@echo "Starting server..."
	@./$(SERVER_BINARY) & SERVER_PID=$$!; \
	sleep 2; \
	echo "Running client..."; \
	./$(CLIENT_BINARY); \
	echo "Stopping server..."; \
	kill $$SERVER_PID

# Run Go tests
.PHONY: test
test:
	go test -v ./...

# Clean up
.PHONY: clean
clean:
	rm -f $(SERVER_BINARY) $(CLIENT_BINARY)
	rm -rf chunks