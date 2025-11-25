#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$ROOT/bin/sandstore"
CLIENT="$ROOT/bin/posix_client"
RUN="$ROOT/run/posix"

# Cleanup
rm -rf "$RUN"
mkdir -p "$RUN"/{node1,node2,node3}

# Build
echo "Building..."
go build -o "$BIN" ./cmd/sandstore
go build -o "$CLIENT" ./clients/posix_client

# Start Cluster (3 Nodes)
SEEDS="127.0.0.1:9001,127.0.0.1:9002,127.0.0.1:9003"

start_node() {
    local id=$1
    local port=$2
    echo "Starting $id on $port..."
    "$BIN" \
        --server posix \
        --node-id "$id" \
        --listen ":$port" \
        --data-dir "$RUN/$id" \
        --seeds "$SEEDS" > "$RUN/$id/stdout.log" 2>&1 &
}

start_node node1 9001
start_node node2 9002
start_node node3 9003

echo "Cluster started."
echo "Logs are in $RUN/nodeX/stdout.log"
echo "Wait a few seconds for leader election..."
sleep 5

# Instructions
echo "---------------------------------------------------"
echo "To test manually:"
echo "1. Find Root Inode ID:"
echo "   grep 'Bootstrapped Root Inode' $RUN/node*/logs/*.log"
echo ""
echo "2. Run Client Commands:"
echo "   $CLIENT --op fsinfo"
echo "   $CLIENT --op create --root <ROOT_ID> --path myfile"
echo "   $CLIENT --op write --root <ROOT_ID> --path myfile --data 'Hello POSIX'"
echo "   $CLIENT --op read --root <ROOT_ID> --path myfile"
echo "---------------------------------------------------"
echo "Press Ctrl+C to stop cluster."

wait