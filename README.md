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

</div>

**A modular distributed file storage system for learning and experimentation**

Sandstore is a production-inspired distributed file storage system built in Go, designed as a hands-on learning playground for students and engineers exploring distributed systems concepts. Think of it as your personal sandbox for understanding how systems like Google File System (GFS) and Hadoop Distributed File System (HDFS) work under the hood.

## Why Sandstore?

Distributed systems can feel abstract and intimidating. Sandstore bridges that gap by providing:

- **Real implementations** of core distributed systems concepts (consensus, replication, fault tolerance)
- **Modular architecture** that lets you experiment with different components independently  
- **Production-grade patterns** in a learner-friendly environment
- **Hands-on experience** with Raft consensus, leader election, and log replication
- **Safe experimentation** - break things, fix them, and learn without consequences

Whether you're a student diving into distributed systems for the first time or an experienced engineer wanting to understand these concepts more deeply, Sandstore gives you a real system to tinker with.

## Features

### Core Distributed Storage
- **Chunked file storage** with configurable chunk sizes (default: 8MB)
- **Automatic file splitting** and reassembly across multiple nodes
- **Metadata management** for file locations and chunk mappings
- **Multi-node cluster** support with health monitoring

### Consensus & Replication
- **Raft consensus algorithm** for metadata consistency
- **Leader election** with automatic failover
- **Log replication** across cluster nodes
- **Chunk replication** for data durability
- **Fault tolerance** with node failure detection

### Modular Services
- **FileService** - High-level file operations (store, read, delete)
- **ChunkService** - Low-level chunk storage and retrieval  
- **MetadataService** - File metadata and chunk location tracking
- **ClusterService** - Node discovery and health management
- **Communication** - gRPC-based inter-node messaging

### Developer Experience
- **Multiple server configurations** (basic, replicated, Raft-enabled)
- **Comprehensive logging** for debugging and learning
- **Test scenarios** for failure simulation
- **MCP integration** for enhanced tooling support

## Quick Start

### Server Options

Sandstore provides three server configurations for different learning stages:

1. **Basic server** (single node, no replication):
   ```bash
   make server-basic
   ```

2. **Replicated server** (3-node cluster with replication):
   ```bash
   make server-replicated
   ```

3. **Raft server** (3-node cluster with Raft consensus):
   ```bash
   make server-raft
   # or simply:
   make server
   ```

### Basic Usage

1. **Start a server** (choose your complexity level):
   ```bash
   make server-raft  # Recommended for full distributed experience
   ```

2. **Store a file from another terminal:**
   ```bash
   make client
   ```

3. **Watch the magic happen:**
   - Leader election occurs automatically (Raft mode)
   - File gets chunked and distributed
   - Metadata is replicated via consensus
   - Check logs in `./logs/` to see the distributed coordination

### Example Client Code

```go
// Store a file
storeRequest := communication.StoreFileRequest{
    Path: "my-document.txt",
    Data: []byte("Hello, distributed world!"),
}

// The system automatically:
// 1. Chunks the file (if large enough)
// 2. Replicates chunks across nodes  
// 3. Updates metadata via Raft consensus
// 4. Returns success once committed
```

## Architecture Overview

Sandstore follows a **layered, service-oriented architecture**:

### Service Layer
- **File Service**: Handles high-level file operations, coordinates chunking
- **Chunk Service**: Manages physical chunk storage on local disk
- **Metadata Service**: Tracks file-to-chunk mappings and locations
- **Cluster Service**: Maintains node membership and health status

### Consensus Layer  
- **Raft Implementation**: Ensures metadata consistency across nodes
- **Leader Election**: Automatic leader selection and failover
- **Log Replication**: Distributes state changes to all nodes

### Communication Layer
- **gRPC Protocol**: Type-safe, efficient inter-node communication
- **Message Routing**: Handles request forwarding and response aggregation
- **Health Checking**: Monitors node availability and network partitions

### Storage Layer
- **Local Disk Storage**: Chunks stored as individual files
- **Configurable Paths**: Separate storage per node instance
- **Atomic Operations**: Ensures data consistency during writes

## Roadmap

### Current Focus
- [ ] Enhanced Raft implementation with log compaction
- [ ] Improved failure detection and recovery
- [ ] Performance benchmarking and optimization
- [ ] Web-based cluster monitoring dashboard

### Future Enhancements  
- [ ] Erasure coding for efficient storage
- [ ] Dynamic cluster membership changes
- [ ] Cross-datacenter replication
- [ ] Compression and deduplication
- [ ] REST API alongside gRPC

### Learning Resources
- [ ] Interactive tutorials for each distributed systems concept
- [ ] Failure scenario simulations
- [ ] Performance analysis tools
- [ ] Architecture deep-dive documentation

## Contributing

We welcome contributions from learners and experts alike! Whether you're fixing bugs, adding features, improving documentation, or sharing learning resources, your contributions help make distributed systems more accessible.

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed guidelines on:
- Setting up your development environment
- Code style and testing requirements  
- Submitting issues and pull requests
- Joining our community discussions

## 📚 Learning Path

New to distributed systems? Try this progression:

1. **Start simple**: Run `make server-basic` and understand file chunking
2. **Add replication**: Try `make server-replicated` to see chunk replication
3. **Dive into Raft**: Use `make server-raft` for leader election and consensus
4. **Simulate failures**: Kill nodes and see how the system recovers
5. **Extend the system**: Add your own features and improvements

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Questions?

- **Issues**: Found a bug or have a feature request? [Open an issue](https://github.com/AnishMulay/sandstore/issues)
- **Discussions**: Want to chat about distributed systems? [Start a discussion](https://github.com/AnishMulay/sandstore/discussions)
- **Learning**: Stuck on a concept? We're here to help - don't hesitate to ask!

---
