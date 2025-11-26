#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$ROOT/bin/sandstore"
CLIENT="$ROOT/bin/posix_client"
RUN="$ROOT/run/posix"

# Cleanup
rm -rf "$RUN"
mkdir -p "$RUN"

# Build
echo "Building..."
go build -o "$BIN" ./cmd/sandstore
go build -o "$CLIENT" ./clients/posix_client

# Start Cluster (3 Nodes)
SEEDS="127.0.0.1:9001,127.0.0.1:9002,127.0.0.1:9003"

start_node() {
    local addr=$1  # e.g., 127.0.0.1:9001
    local dir_name=$(echo "$addr" | tr ':' '_')
    echo "Starting $addr..."
    mkdir -p "$RUN/$dir_name"
    "$BIN" \
        --server posix \
        --node-id "$addr" \
        --listen "$addr" \
        --data-dir "$RUN/$dir_name" \
        --seeds "$SEEDS" > "$RUN/$dir_name/stdout.log" 2>&1 &
}

start_node 127.0.0.1:9001
start_node 127.0.0.1:9002
start_node 127.0.0.1:9003

echo "Cluster started."
echo "Logs are in $RUN/nodeX/stdout.log"
echo "Waiting for cluster to elect a leader..."
sleep 8 # Wait a bit longer to be safe, client is now self-sufficient

# Run automated client test
echo "---------------------------------------------------"
echo "Running automated client test..."
# The client will now discover the root inode ID on its own.
$CLIENT
echo "---------------------------------------------------"
echo "Press Ctrl+C to stop cluster."

wait
