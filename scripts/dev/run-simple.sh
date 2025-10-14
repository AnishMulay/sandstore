#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

NODE_ID="${NODE_ID:-simple-node}"
LISTEN_ADDR="${LISTEN_ADDR:-:8080}"
DATA_ROOT="${DATA_ROOT:-$ROOT/run/simple}"
DATA_DIR="${DATA_DIR:-$DATA_ROOT/$NODE_ID}"

mkdir -p "$DATA_DIR"

echo "Starting simple server (node-id=${NODE_ID}, listen=${LISTEN_ADDR}, data-dir=${DATA_DIR})"
echo "Press Ctrl-C to stop."

exec go run ./cmd/sandstore \
  --server simple \
  --node-id "${NODE_ID}" \
  --listen "${LISTEN_ADDR}" \
  --data-dir "${DATA_DIR}"
