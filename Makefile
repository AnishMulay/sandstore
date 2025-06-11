# Variables
BINARY_NAME=sandstore
MAIN_PATH=./cmd/server

# Default target
.PHONY: all
all: build

# Build the application
.PHONY: build
build:
	go build -o $(BINARY_NAME) $(MAIN_PATH)

# Run the application
.PHONY: run
run: build
	./$(BINARY_NAME)

# Clean up binary files
.PHONY: clean
clean:
	go clean
	rm -f $(BINARY_NAME)

# Test the application
.PHONY: test
test:
	go test ./...