#!/bin/bash

# Build both binaries
make build

# Start the server in the background
echo "Starting server..."
./server &
SERVER_PID=$!

# Wait for server to initialize
echo "Waiting for server to initialize..."
sleep 2

# Run the client
echo "Running client..."
./client

# Wait for client to finish
sleep 1

# Kill the server
echo "Stopping server..."
kill $SERVER_PID

echo "Test completed."