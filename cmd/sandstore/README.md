# Sandstore Server

Single-process entrypoint for starting one Sandstore node.

## Flags

- `--server=raft` - Server type (default: raft)
- `--node-id` - Unique node identifier (required)
- `--listen` - Listen address (default: :8080)
- `--data-dir` - Data directory (default: ./data)
- `--seeds` - Comma-separated seed peers for cluster formation
- `--bootstrap` - Bootstrap cluster (use on first node only)

Clusters are formed by starting multiple processes with different node IDs. See `scripts/dev/run-5.sh` for an example.