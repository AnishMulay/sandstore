#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$ROOT/bin/sandstore"
RUN="$ROOT/run"
mkdir -p "$RUN"/{node1,node2,node3,node4,node5}

# Build once
go build -o "$BIN" ./cmd/sandstore

# On exit / Ctrl-C, kill all children started by this script
cleanup() {
  pkill -P $$ 2>/dev/null || true       # SIGTERM children
  sleep 1
  # If anything still alive, force-kill
  pkill -9 -P $$ 2>/dev/null || true
}
trap cleanup INT TERM EXIT

SEEDS="127.0.0.1:8101,127.0.0.1:8102,127.0.0.1:8103,127.0.0.1:8104,127.0.0.1:8105"

start_node() {
  local id="$1" port="$2" extra="${3:-}"
  local dir="$RUN/$id"
  mkdir -p "$dir"
  "$BIN" \
    --server=raft \
    --node-id="$id" \
    --listen=":$port" \
    --data-dir="$dir" \
    $extra \
    --seeds="$SEEDS" &
}

start_node node1 8101 "--bootstrap=true"
start_node node2 8102
start_node node3 8103
start_node node4 8104
start_node node5 8105

echo "Sandstore 5-node cluster running."
echo "Sandstore logs: $RUN/node*/logs/*.log"
echo "Press Ctrl-C to stop all nodes."
wait   # keep the script in the foreground; trap handles cleanup
