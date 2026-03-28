#!/usr/bin/env bash
set -e

# Usage: Ensure you have started etcd first:
# docker compose -f deploy/docker/etcd/docker-compose.yaml up -d

ETCD_CONTAINER="${ETCD_CONTAINER:-sandstore-etcd}"
# Comma-separated entries in id@address format.
# Backward compatible default keeps local bare-metal behavior.
CLUSTER_NODES="${CLUSTER_NODES:-127.0.0.1:9001@127.0.0.1:9001,127.0.0.1:9002@127.0.0.1:9002,127.0.0.1:9003@127.0.0.1:9003}"

echo "Waiting for etcd to be ready..."
# Wait loop to ensure the container is responsive
for i in {1..10}; do
  if docker exec "$ETCD_CONTAINER" etcdctl endpoint health > /dev/null 2>&1; then
    break
  fi
  echo "Waiting for etcd..."
  sleep 1
done

ETCD_CMD="docker exec $ETCD_CONTAINER etcdctl"

# Clear existing config to ensure a clean slate
echo "Clearing old configuration..."
$ETCD_CMD del --prefix /sandstore/config/nodes/

echo "Bootstrapping static cluster membership (3 Nodes)..."

# For each CLUSTER_NODES entry:
# - if format is id@address, use those explicitly
# - if no @ is present, id=address

IFS=',' read -r -a entries <<< "$CLUSTER_NODES"
for entry in "${entries[@]}"; do
  id="${entry%%@*}"
  address="${entry#*@}"
  if [[ "$entry" != *"@"* ]]; then
    address="$id"
  fi

  payload=$(printf '{"id":"%s","address":"%s","role":"voter"}' "$id" "$address")
  $ETCD_CMD put "/sandstore/config/nodes/$id" "$payload"
done

echo "Cluster bootstrapped successfully."
