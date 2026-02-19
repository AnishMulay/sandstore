#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RUN="$ROOT/run/cluster"
BIN="$ROOT/bin/sandstore"
SMOKE="$ROOT/bin/open-smoke"

PIDS=()
TEST_STATUS="failed"

is_port_listening() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
    return $?
  fi

  if command -v nc >/dev/null 2>&1; then
    nc -z localhost "$port" >/dev/null 2>&1
    return $?
  fi

  return 1
}

ports_in_use() {
  for port in 9001 9002 9003; do
    if is_port_listening "$port"; then
      return 0
    fi
  done
  return 1
}

cleanup() {
  set +e
  if [[ ${#PIDS[@]} -gt 0 ]]; then
    echo "Stopping cluster..."
    kill "${PIDS[@]}" >/dev/null 2>&1 || true
    wait "${PIDS[@]}" >/dev/null 2>&1 || true
  fi

  # Give processes a moment to release listeners.
  for _ in {1..20}; do
    if ! ports_in_use; then
      break
    fi
    sleep 0.25
  done

  if ports_in_use; then
    echo "WARNING: one or more cluster ports are still in use (9001/9002/9003)."
  else
    echo "Ports 9001/9002/9003 are clear."
  fi

  echo "Result: $TEST_STATUS"
}
trap cleanup EXIT INT TERM

require_etcd() {
  local etcd_up=1
  if command -v nc >/dev/null 2>&1; then
    if nc -z localhost 2379 >/dev/null 2>&1; then
      etcd_up=0
    fi
  else
    if (exec 3<>/dev/tcp/localhost/2379) >/dev/null 2>&1; then
      exec 3<&-
      exec 3>&-
      etcd_up=0
    fi
  fi

  if [[ $etcd_up -ne 0 ]]; then
    echo "etcd is not reachable on localhost:2379"
    echo "Start it first, for example: docker compose -f deploy/docker/etcd/docker-compose.yaml up -d"
    exit 1
  fi
}

build_binaries() {
  echo "Building binaries..."
  mkdir -p "$ROOT/bin"
  go build -o "$BIN" ./cmd/sandstore
  go build -o "$SMOKE" ./clients/open_smoke
}

bootstrap_cluster_config() {
  echo "Bootstrapping etcd node config..."
  "$ROOT/scripts/dev/init-etcd.sh"
}

start_node() {
  local addr="$1"
  local dir_name
  dir_name="$(echo "$addr" | tr ':' '_')"

  mkdir -p "$RUN/$dir_name"
  echo "Starting node $addr"

  "$BIN" \
    --server node \
    --node-id "$addr" \
    --listen "$addr" \
    --data-dir "$RUN/$dir_name" >"$RUN/$dir_name/stdout.log" 2>&1 &

  PIDS+=("$!")
}

wait_for_leader() {
  local timeout_secs=45
  local start_ts
  start_ts="$(date +%s)"

  echo "Waiting for leader election..." >&2
  while true; do
    for addr in 127.0.0.1:9001 127.0.0.1:9002 127.0.0.1:9003; do
      local dir_name
      local log_file
      dir_name="$(echo "$addr" | tr ':' '_')"
      log_file="$RUN/$dir_name/logs/$addr.log"

      if [[ -f "$log_file" ]] && grep -q "Became Leader" "$log_file"; then
        echo "$addr"
        return 0
      fi
    done

    if (( $(date +%s) - start_ts >= timeout_secs )); then
      echo "Timed out waiting for leader election after ${timeout_secs}s" >&2
      return 1
    fi

    sleep 1
  done
}

main() {
  require_etcd

  if ports_in_use; then
    echo "Ports 9001/9002/9003 are already in use. Stop existing sandstore processes first."
    exit 1
  fi

  bootstrap_cluster_config

  rm -rf "$RUN"
  mkdir -p "$RUN"

  build_binaries

  start_node 127.0.0.1:9001
  start_node 127.0.0.1:9002
  start_node 127.0.0.1:9003

  leader="$(wait_for_leader)"
  echo "Leader elected: $leader"

  echo "Running sandlib Open smoke test..."
  SANDSTORE_ADDR="$leader" "$SMOKE"

  TEST_STATUS="passed"
  echo "Smoke test finished successfully."
}

main "$@"
