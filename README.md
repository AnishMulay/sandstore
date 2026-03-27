<div align="center">

# Sandstore

![Go Version](https://img.shields.io/github/go-mod/go-version/AnishMulay/sandstore?style=flat-square&logo=go&color=00ADD8)
![License](https://img.shields.io/badge/License-MIT-blue?style=flat-square&logo=opensource)
![Language](https://img.shields.io/github/languages/top/AnishMulay/sandstore?style=flat-square&logo=go&color=00ADD8)
![Last Commit](https://img.shields.io/github/last-commit/AnishMulay/sandstore?style=flat-square)
![Commit Activity](https://img.shields.io/github/commit-activity/m/AnishMulay/sandstore?style=flat-square)

![Repo Size](https://img.shields.io/github/repo-size/AnishMulay/sandstore?style=flat-square)
![Issues](https://img.shields.io/github/issues/AnishMulay/sandstore?style=flat-square)
![Contributors](https://img.shields.io/github/contributors/AnishMulay/sandstore?style=flat-square)
[![Website](https://img.shields.io/badge/Website-sandstore--eta.vercel.app-black?style=flat-square&logo=vercel)](https://sandstore-eta.vercel.app/)

<img width="300" height="300" alt="sandstore_logo" src="https://github.com/user-attachments/assets/509e0bb5-7fab-409f-ba59-a2b161b90923" />

**A modular framework for building and experimenting with distributed storage architectures.**

[sandstore-eta.vercel.app](https://sandstore-eta.vercel.app/)

</div>

Sandstore lets you assemble a distributed storage system from well-defined, swappable components — choose your metadata engine, consensus mechanism, chunk storage, cluster membership, and transport — then deploy and test the result against a real multi-node cluster in minutes.

It started as a way to learn distributed systems internals by building them. It has grown into a platform for experimenting with how fundamental architectural decisions change the behavior of a storage system.

## Why Sandstore?

Most distributed storage systems bake their architecture in. The topology — where metadata lives, how replication works, how nodes discover each other — is a fixed decision made at design time.

Sandstore treats topology as a variable.

The system is built around two top-level orchestration interfaces: a **ControlPlaneOrchestrator** that owns metadata, placement, and consensus coordination, and a **DataPlaneOrchestrator** that owns chunk movement, replica fanout, and read failover. Everything beneath those interfaces is swappable. To build a new topology, you implement new versions of the components that matter for your design and wire them together. The server layer, client, and deploy tooling stay the same.

This makes Sandstore useful for:

- **Students** who want to go beyond reading about distributed systems and actually run them
- **Researchers** who want to experiment with how design decisions affect system behavior
- **Engineers** who want a clean, readable reference implementation of production distributed storage patterns

## Current Architecture

The active topology today is a **hyperconverged node** — every node runs both the control and data plane, similar in spirit to CockroachDB. There are no separate metadata servers. Cluster membership is handled by etcd.

Each node is assembled from:

| Layer | Interface | Active Implementation |
|---|---|---|
| Cluster membership | `ClusterService` | etcd |
| Transport | `Communicator` | gRPC |
| Metadata storage | `MetadataService` | BoltDB |
| Metadata consensus | `MetadataReplicator` | Durable Raft (WAL + CRC) |
| Chunk storage | `ChunkService` | Local disk |
| Control coordination | `ControlPlaneOrchestrator` | Raft-backed control plane |
| Data coordination | `DataPlaneOrchestrator` | Raft-aware data plane |
| Placement | `PlacementStrategy` | Sorted placement |
| Routing | `EndpointResolver` | Static endpoint resolver |
| Write coordination | `TransactionCoordinator` | Raft transaction coordinator |

The server layer (`HyperconvergedServer`) depends only on the orchestrator interfaces, not on any concrete implementation. This is the seam where new topologies plug in.

The canonical entry point for understanding the system is `servers/node/topology_hyperconverged.go`. It assembles every component in dependency order and shows exactly how the current topology is built.

## Quick Start

**Prerequisites:** Go 1.24+, Docker with Compose, Bash, free ports 2379, 2380, 9001–9003

**Start a 3-node local cluster:**

```bash
git clone https://github.com/AnishMulay/sandstore
cd sandstore

# Start etcd
docker compose -f deploy/docker/etcd/docker-compose.yaml up -d

# Build, start 3 nodes, elect a leader, run smoke test
./scripts/dev/run-smoke.sh
```

This boots a full 3-node Sandstore cluster on localhost, waits for leader election, and runs an end-to-end open/read/write/fsync/remove smoke test against it. The cluster stays up after the script finishes for manual exploration.

**Kubernetes (full integration suite):**

```bash
make test-cluster
```

Builds Docker images, deploys a 3-node cluster to Kubernetes, and runs leader election, open/read/write, restart durability, leader deletion recovery, and node rejoin tests. Cleans up the namespace on completion.

**Other useful make targets:**

```bash
make build            # Build the node binary
make proto            # Regenerate protobuf/gRPC stubs
make durability-smoke # Ephemeral Docker durability test (bring-up + test + teardown)
make client           # Build the manual client binary
```

## Repository Layout

```
cmd/sandstore/         # Node binary entrypoint
servers/node/          # Active topology wiring (start here)
internal/
  orchestrators/       # ControlPlaneOrchestrator, DataPlaneOrchestrator and their interfaces
  metadata_service/    # MetadataService interface + BoltDB implementation
  metadata_replicator/ # MetadataReplicator interface + Durable Raft implementation
  chunk_service/       # ChunkService interface + local disk implementation
  cluster_service/     # ClusterService interface + etcd implementation
  communication/       # Communicator interface + gRPC implementation
  server/              # Server interface + HyperconvergedServer
clients/
  library/             # SDK, smart client, topology router (HyperconvergedRouter)
  open_smoke/          # End-to-end smoke test client
  durability_smoke/    # Failover/durability smoke client
  mcp/                 # Model Context Protocol server (in progress)
deploy/
  docker/              # Docker Compose configs for local clusters
  k8s/                 # Kubernetes manifests for production-like testing
integration/cluster/   # Kubernetes integration test suite
docs/                  # Design documents
proto/                 # Protobuf source definitions
```

## Implementing a New Topology

To build a new storage topology — say, a GFS-style architecture with a dedicated metadata server — you implement new versions of the interfaces relevant to your design:

- **`PlacementStrategy`** — placement logic for your node roles
- **`DataPlaneOrchestrator`** — your write/read semantics (e.g. primary/secondary instead of replicated-prepare + Raft-commit)
- **`TransactionCoordinator`** — coordination logic matching your write path
- Optionally: **`MetadataService`**, **`MetadataReplicator`** — if you want different metadata persistence or consensus behavior

Then write a new wiring file (like `servers/node/topology_hyperconverged.go`) that assembles your implementations and passes them to `HyperconvergedServer`. No changes to the server layer, client, or deploy tooling required.

The interface definitions live in `internal/orchestrators/interfaces.go`. Start there.

## Roadmap

### Active
- [x] Hyperconverged node topology (etcd + gRPC + durable Raft + BoltDB)
- [x] Durable Raft WAL with CRC/envelope protection and corruption recovery
- [x] Kubernetes integration test suite (leader election, durability, node rejoin)
- [x] Smart client with topology-aware leader routing (HyperconvergedRouter)
- [x] 2PC transactional chunk writes

### In Progress
- [ ] GFS-style separated metadata/data topology (second reference implementation)
- [ ] MCP server aligned with current server message types
- [ ] FUSE client (`clients/fuse/`)

### Planned
- [ ] Additional PlacementStrategy implementations
- [ ] Additional storage backends (object storage semantics)
- [ ] Interactive demo
- [ ] Log compaction and snapshot-based cluster recovery improvements
- [ ] Web-based cluster monitoring

## Contributing

Contributions are welcome — new topology implementations especially so.

The best place to start is `servers/node/topology_hyperconverged.go` to understand the current topology, then `internal/orchestrators/interfaces.go` to understand the extension points.

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on setting up your environment, code style, and submitting pull requests.

**Questions?**
- [Open an issue](https://github.com/AnishMulay/sandstore/issues) for bugs or feature requests
- [Start a discussion](https://github.com/AnishMulay/sandstore/discussions) for architecture questions or ideas

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.

---
