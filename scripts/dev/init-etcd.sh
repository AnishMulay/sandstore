#!/usr/bin/env bash
set -e

# Usage: Ensure you have started etcd first:
# docker compose -f deploy/docker/etcd/docker-compose.yaml up -d

echo "Waiting for etcd to be ready..."
# Wait loop to ensure the container is responsive
for i in {1..10}; do
  if docker exec sandstore-etcd etcdctl endpoint health > /dev/null 2>&1; then
    break
  fi
  echo "Waiting for etcd..."
  sleep 1
done

ETCD_CMD="docker exec sandstore-etcd etcdctl"

# Clear existing config to ensure a clean slate
echo "Clearing old configuration..."
$ETCD_CMD del --prefix /sandstore/config/nodes/

echo "Bootstrapping static cluster membership (3 Nodes)..."

# Note: In your run-cluster.sh, the Node ID is the same as the Address (127.0.0.1:900x)
# We replicate that exact structure here so the nodes can find themselves.

$ETCD_CMD put /sandstore/config/nodes/127.0.0.1:9001 '{"id":"127.0.0.1:9001","address":"127.0.0.1:9001","role":"voter"}'
$ETCD_CMD put /sandstore/config/nodes/127.0.0.1:9002 '{"id":"127.0.0.1:9002","address":"127.0.0.1:9002","role":"voter"}'
$ETCD_CMD put /sandstore/config/nodes/127.0.0.1:9003 '{"id":"127.0.0.1:9003","address":"127.0.0.1:9003","role":"voter"}'

echo "Cluster bootstrapped successfully."