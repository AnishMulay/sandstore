#!/usr/bin/env bash
set -e

# Usage: Ensure you have started etcd first:
# docker compose -f deploy/docker/etcd/docker-compose.yaml up -d

echo "Waiting for etcd to be ready..."
# Simple wait loop to ensure socket is open
for i in {1..10}; do
  if docker exec sandstore-etcd etcdctl endpoint health > /dev/null 2>&1; then
    break
  fi
  echo "Waiting for etcd..."
  sleep 1
done

ETCD_CMD="docker exec sandstore-etcd etcdctl"

# Clear existing config
echo "Clearing old configuration..."
$ETCD_CMD del --prefix /sandstore/config/nodes/

# Add static configuration for the 5 nodes
# This matches the ports in scripts/dev/run-5.sh (8101-8105)
echo "Bootstrapping static cluster membership..."

$ETCD_CMD put /sandstore/config/nodes/node1 '{"id":"node1","address":"localhost:8101","role":"voter"}'
$ETCD_CMD put /sandstore/config/nodes/node2 '{"id":"node2","address":"localhost:8102","role":"voter"}'
$ETCD_CMD put /sandstore/config/nodes/node3 '{"id":"node3","address":"localhost:8103","role":"voter"}'
$ETCD_CMD put /sandstore/config/nodes/node4 '{"id":"node4","address":"localhost:8104","role":"voter"}'
$ETCD_CMD put /sandstore/config/nodes/node5 '{"id":"node5","address":"localhost:8105","role":"voter"}'

echo "Cluster bootstrapped successfully."