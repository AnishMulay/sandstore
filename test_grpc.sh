#!/bin/bash

# Build the server and client
echo "Building server and client..."
go build -o server ./cmd/server
go build -o client ./cmd/client

# Start the gRPC server in the background
echo "Starting gRPC server on port 8081..."
./server -grpc -addr=:8081 &
SERVER_PID=$!

# Give the server time to start
sleep 2

# Run the gRPC client
echo "Running gRPC client..."
./client -grpc -server=localhost:8081

# Kill the server
echo "Stopping server..."
kill $SERVER_PID

echo "Test completed!"