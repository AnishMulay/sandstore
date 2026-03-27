#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$ROOT/bin/sandstore"
CLIENT="$ROOT/bin/client"
RUN="$ROOT/run/cluster"

# Cleanup data from previous runs
rm -rf "$RUN"
mkdir -p "$RUN"

# Build binaries
echo "Building..."
go build -o "$BIN" ./cmd/sandstore
go build -o "$CLIENT" ./clients/client

# Start Cluster (3 Nodes)
start_node() {
    local addr=$1  # e.g., 127.0.0.1:9001
    local dir_name=$(echo "$addr" | tr ':' '_')
    echo "Starting node $addr..."
    mkdir -p "$RUN/$dir_name"
    
    # We use '--server node' because cmd/sandstore/main.go expects "node"
    # This launches the SimpleServer which is Posix-compliant.
    "$BIN" \
        --server node \
        --node-id "$addr" \
        --listen "$addr" \
        --data-dir "$RUN/$dir_name" > "$RUN/$dir_name/stdout.log" 2>&1 &
}

start_node 127.0.0.1:9001
start_node 127.0.0.1:9002
start_node 127.0.0.1:9003

echo "Cluster started."
echo "Logs are in $RUN/nodeX/stdout.log"
echo "Waiting for cluster to register and elect a leader..."
sleep 8 

# Run automated client test
echo "---------------------------------------------------"
echo "Running automated client test..."
# The client connects to 127.0.0.1:9001 by default
$CLIENT
echo "---------------------------------------------------"
echo "Press Ctrl+C to stop cluster."

wait