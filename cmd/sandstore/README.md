# Sandstore Server

Single-process entry point for starting one Sandstore node. The runtime behaviour is selected with the `--server` flag.

## Flags

- `--server` - Server type (`simple` or `raft`, default: `raft`)
- `--node-id` - Unique node identifier (required)
- `--listen` - Listen address (default: `:8080`)
- `--data-dir` - Data directory (default: `./data`)
- `--seeds` - Comma-separated seed peers for cluster formation (`raft` mode)
- `--bootstrap` - Bootstrap cluster (use on first Raft node only)

### Quick Recipes

Start a single-node simple server:

```bash
go run ./cmd/sandstore \
  --server simple \
  --node-id simple-node \
  --listen :8080 \
  --data-dir ./run/simple/simple-node
```

Start a Raft node (run multiple instances with different IDs and ports):

```bash
go run ./cmd/sandstore \
  --server raft \
  --node-id node1 \
  --listen :8101 \
  --data-dir ./run/raft/node1 \
  --bootstrap \
  --seeds "127.0.0.1:8101,127.0.0.1:8102,127.0.0.1:8103"
```

See `scripts/dev/run-simple.sh` and `scripts/dev/run-5.sh` for ready-made launchers.
