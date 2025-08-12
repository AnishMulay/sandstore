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

```bash
make build
```

This creates two executables:
- `server` - The Sandstore server binary
- `client` - A test client for interacting with the server

### 5. Verify Installation

Run the test to ensure everything is working:

```bash
make test-server
```

This will:
1. Start a 3-node Raft cluster
2. Run a client that stores a test file
3. Verify the distributed storage works correctly
4. Clean up automatically

You should see output indicating successful leader election and file storage.

## Development Setup

### Directory Structure

After installation, your directory should look like this:

```
sandstore/
├── cmd/                    # Application entry points
│   ├── server/            # Server main and configurations
│   ├── client/            # Client examples and test scenarios  
│   └── mcp/               # MCP server integration
├── internal/              # Internal packages (not importable)
│   ├── chunk_service/     # Chunk storage implementation
│   ├── file_service/      # High-level file operations
│   ├── metadata_service/  # File metadata management
│   ├── cluster_service/   # Node membership and Raft
│   └── communication/     # gRPC communication layer
├── proto/                 # Protocol buffer definitions
├── gen/                   # Generated protobuf code
├── logs/                  # Runtime logs (created automatically)
├── chunks/                # Chunk storage (created automatically)
└── config.yaml           # Cluster configuration
```

### Running Individual Components

**Start just the server cluster:**
```bash
make server
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

Test the full server-client interaction:

```bash
make test-server
```

### Manual Testing

1. **Start the servers** in one terminal:
   ```bash
   make server
   ```

2. **In another terminal**, interact with the cluster:
   ```bash
   # Store a file
   echo "Hello, Sandstore!" > test.txt
   # Use the client examples in cmd/client/ for more complex operations
   ```

3. **Monitor the logs** to see distributed coordination:
   ```bash
   tail -f logs/8080/sandstore.log
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