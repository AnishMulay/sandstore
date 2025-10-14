# Installation Guide

This guide will help you set up Sandstore locally for development and experimentation.

## Prerequisites

Before you begin, ensure you have the following installed on your system:

### Required Software

- **Go 1.24.2 or later**
  - Download from [golang.org](https://golang.org/download/)
  - Verify installation: `go version`

- **Git**
  - Most systems have this pre-installed
  - Verify installation: `git --version`

- **Protocol Buffers Compiler (protoc)**
  - **macOS**: `brew install protobuf`
  - **Ubuntu/Debian**: `sudo apt install protobuf-compiler`
  - **Windows**: Download from [protobuf releases](https://github.com/protocolbuffers/protobuf/releases)
  - Verify installation: `protoc --version`

### Go Tools

Install the required Go tools for protocol buffer generation:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Ensure your `$GOPATH/bin` is in your `$PATH`:

```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

## Installation Steps

### 1. Clone the Repository

```bash
git clone https://github.com/AnishMulay/sandstore.git
cd sandstore
```

### 2. Install Dependencies

```bash
go mod download
```

This will download all required Go modules including:
- gRPC and Protocol Buffers
- UUID generation
- YAML configuration parsing
- MCP (Model Context Protocol) support

### 3. Generate Protocol Buffer Files

```bash
make proto
```

This generates the gRPC client and server code from the `.proto` files in the `proto/` directory.

### 4. Build the Project

You can build the main server and optional tools individually:

```bash
go build -o bin/sandstore ./cmd/sandstore
go build -o bin/sandstore-client ./cmd/client
go build -o bin/sandstore-mcp ./cmd/mcp
```

> `cmd/sandstore` is the only server entry point. Different behaviours (simple vs Raft) are selected with flags at runtime.

### 5. Verify Installation

Start a simple node and run the client to verify end-to-end behaviour:

```bash
# Terminal 1
make simple

# Terminal 2
make client
```

For a multi-node Raft demonstration, run the 5-node script instead of `make simple`:

```bash
make cluster
```

Logs are written under `run/logs/` while the script is running.

## Development Setup

### Directory Structure

After installation, your directory should look like this:

```
sandstore/
├── cmd/                    # Application entry points
│   ├── sandstore/         # Unified server entry point (flags select simple/Raft)
│   ├── client/            # Test client
│   └── mcp/               # MCP server integration
├── servers/               # Server compositions
│   ├── simple/            # Simple single-node wiring
│   └── raft/              # Raft-backed node wiring
├── internal/              # Internal packages (not importable)
│   ├── chunk_service/     # Chunk storage implementation
│   ├── file_service/      # High-level file operations
│   ├── metadata_service/  # File metadata management
│   ├── cluster_service/   # Node membership and Raft
│   └── communication/     # gRPC communication layer
├── proto/                 # Protocol buffer definitions
├── gen/                   # Generated protobuf code
├── run/                   # Runtime data/logs created by helper scripts
└── scripts/               # Utility scripts (dev tooling, demos)
```

### Running Individual Components

**Start different server types:**
```bash
# Simple server (single node, no replication)
make simple

# Raft demo cluster (5-node example)
make cluster

# Manual single node with custom flags
go run ./cmd/sandstore \
  --server raft \
  --node-id node1 \
  --listen :8080 \
  --data-dir ./run/raft/node1 \
  --bootstrap \
  --seeds "127.0.0.1:8080"
```

**Run the test client:**
```bash
make client
```

**Build the MCP server:**
```bash
make mcp
```

### Configuration

The default configuration in `config.yaml` sets up a 3-node cluster:

```yaml
communicator:
    type: grpc
servers:
    - id: server1
      address: localhost:8080
      healthy: true
    - id: server2  
      address: localhost:8081
      healthy: true
    - id: server3
      address: localhost:8082
      healthy: true
default_server: server1
```

You can modify this to change ports, add nodes, or adjust cluster settings.

## Running Tests

### Unit Tests

Run the Go test suite:

```bash
make test
```

### Integration Tests

Bring up the demo Raft cluster and exercise it with the client:

```bash
# Terminal 1
make cluster

# Terminal 2
make client
```

### Manual Testing

1. **Start the simple server** (foreground process):
   ```bash
   make simple
   ```

   *or* bring up the full Raft demo cluster:
   ```bash
   make cluster
   ```

2. **In another terminal**, interact with the node or cluster:
   ```bash
   make client
   ```

3. **Monitor the logs** to see distributed coordination:
   ```bash
   tail -f run/logs/node1.log   # Example log from the 5-node cluster
   ```

## Troubleshooting

### Common Issues

**"protoc: command not found"**
- Install Protocol Buffers compiler (see Prerequisites)
- Ensure `protoc` is in your PATH

**"cannot find package" errors**
- Run `go mod download` to install dependencies
- Ensure you're using Go 1.24.2 or later

**Port already in use**
- Check if other processes are using ports 8080-8082
- Kill existing processes: `lsof -ti:8080 | xargs kill`
- Or modify `config.yaml` to use different ports

**Permission denied writing to logs/chunks**
- Ensure the current user has write permissions in the project directory
- The application creates these directories automatically

**Raft leader election fails**
- Ensure all three server ports (8080, 8081, 8082) are available
- Check firewall settings if running across different machines
- Review logs in `logs/*/sandstore.log` for detailed error messages

### Getting Help

If you encounter issues not covered here:

1. **Check the logs** in `logs/[node-id]/sandstore.log` for detailed error messages
2. **Search existing issues** on GitHub
3. **Open a new issue** with:
   - Your operating system and Go version
   - Complete error messages
   - Steps to reproduce the problem

### Clean Up

To clean up build artifacts and runtime data:

```bash
make clean
```

This removes:
- Compiled binaries (`server`, `client`, `sandstore-mcp`)
- Runtime directories (`chunks/`, `logs/`)

## Next Steps

Once you have Sandstore running locally:

1. **Explore the examples** in `cmd/client/` to understand different usage patterns
2. **Read the code** in `internal/` to understand the distributed systems concepts
3. **Try failure scenarios** by killing nodes and observing recovery
4. **Check out [CONTRIBUTING.md](CONTRIBUTING.md)** to start contributing

Happy distributed systems learning!
