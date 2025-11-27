# Feature: POSIX Chunk Service Design

### Process

- [ ] **Goals** → Define the blob storage responsibilities.
- [ ] **User stories** → "Write data" and "Repair data" stories.
- [ ] **Data models** → The Chunk object and physical storage layout.
- [ ] **Nail an MVP** → Read/Write/Delete primitives with Local Storage.
- [ ] **Draw the prototype** → The interaction with the Replicator.
- [ ] **Future** → Erasure Coding, Bitrot Protection, Deduplication.
- [ ] **Drill into specific components** → Interface definitions.
- [ ] **Pick a stack** → Go + OS Filesystem (for MVP).
- [ ] **Actual software development**.

### Goals

#### Vision and Purpose

The `PosixChunkService` manages the lifecycle of raw data blocks.

It decouples the **Logical File** (handled by `FileService`) from the **Physical Storage** (handled here).

It acts as the gateway to the `ChunkReplicator`, ensuring that when data is written, it is durable across multiple nodes.

#### Functional Goals

- **Partial Access**: Support reading and writing sub-ranges of a chunk (e.g., "Write bytes 10-50 of Chunk 99"). This is critical for POSIX performance so we don't rewrite 64KB just to change 1 byte.
- **Abstraction**: The caller (`FileService`) should not know if the chunk is stored on `ext4`, `xfs`, or in RAM.
- **Integrity**: (Future) Guarantee that data read back is exactly what was written (Checksums).

#### Non-Functional Goals

- **Throughput**: This is the I/O bottleneck of the system. The path from network to disk must be zero-copy where possible.
- **Concurrency**: Must handle multiple clients reading/writing different chunks simultaneously without locking the whole store.

### User Stories (Data Focus)

- **Random Write**: As the `FileService`, I want to write `Hello` to Chunk `#500` at offset `1024` without reading back the whole chunk first, so that database workloads (which do random writes) are fast.
- **Replicated Write**: As the `FileService`, when I call `WriteChunk`, I want the service to block until the data is safely replicated to the cluster (e.g., W=3), so I can guarantee durability to the user.
- **Local Retrieval**: As the `ChunkReplicator` (on a peer node), I want to save a received chunk to my local disk, so that I can serve as a replica.
- **Cleanup**: As the Garbage Collector, I want to explicitly `DeleteChunk(#500)` when the file using it is deleted, reclaiming disk space.

### Data Models

#### 1. Chunk (Logical)

The concept passed over the wire.

```go
type ChunkID string // or uint64

type ChunkData struct {
    ID      ChunkID
    Data    []byte
    Offset  int64  // For partial updates
}
```

#### 2. Physical Storage Layout (MVP)

How we store it on the local disk of a server. `root_dir/chunks/xx/yy/chunk_id.dat` (Sharding by directory to avoid too many files in one folder).

### MVP

The MVP focuses on the Interface and a Local Filesystem implementation.

#### API Surface:

- `WriteChunk(id, offset, data)` -> Triggers Replication.
- `ReadChunk(id, offset, length)` -> Reads from Local (or fetches if missing).
- `DeleteChunk(id)` -> Triggers Replication (Delete marker).

#### Key Logic:

- **Read Path**: If `FileService` asks for a chunk, the `ChunkService` first checks local disk. If not found (and if this node is supposed to have it), it returns an error.
- **Write Path**: `ChunkService` does not write to disk immediately. It hands the data to `ChunkReplicator`. The Replicator ensures it reaches the peers, and then calls back to write to disk.

### Prototyping (The Write Flow)

This flow emphasizes that `ChunkService` is the **Coordinator** of data IO.

**Scenario**: `FileService` writes to Chunk `#99`

1.  **Request**: `FileService` calls `ChunkService.WriteChunk(#99, data)`.
2.  **Replication**: `ChunkService` calls `ChunkReplicator.Replicate(#99, data)`.
    > **Note**: Unlike Metadata (Raft), Chunk replication usually sends data to a specific "Chain" or "Placement Group" of nodes, not the whole cluster.
3.  **Transport**: The Replicator sends data to Peer A and Peer B.
4.  **Persistence**:
    - Peer A's Replicator receives data -> Calls Peer A's `ChunkService.WriteLocal(#99)`.
    - Peer B's Replicator receives data -> Calls Peer B's `ChunkService.WriteLocal(#99)`.
5.  **Ack**: Once peers acknowledge, `ChunkService` returns "Success" to `FileService`.

### Project Future

#### Future Extensions

- **Erasure Coding**: Instead of replicating 1 chunk 3 times, split it into 4 data + 2 parity shards. The `ChunkService` logic handles the encoding/decoding transparently.
- **Tiering**: Move old chunks to S3 or cold storage automatically.
- **Bitrot Protection**: Store a `CRC32` checksum alongside the chunk header. Verify it on every Read.

### Specific Components

#### 1. Interface Definition

`sandstore/internal/posix_chunk/chunk_service.go`

```go
package posixchunk

import "context"

// PosixChunkService defines the blob IO operations.
type PosixChunkService interface {
    // WriteChunk writes data to a specific offset in a chunk.
    // This triggers replication to the cluster.
    WriteChunk(ctx context.Context, id string, offset int64, data []byte) error
    
    // ReadChunk reads a range of bytes from a chunk.
    // Usually served locally or proxied if this node doesn't have the data.
    ReadChunk(ctx context.Context, id string, offset int64, length int64) ([]byte, error)
    
    // DeleteChunk removes the chunk from the cluster.
    DeleteChunk(ctx context.Context, id string) error
    
    // WriteLocal is used by the Replicator to force a write to this node's disk.
    // It bypasses replication (to prevent infinite loops).
    WriteLocal(ctx context.Context, id string, offset int64, data []byte) error
}
```

#### 2. Implementation (Local Filesystem)

`sandstore/internal/posix_chunk/local/chunk_service.go`

```go
type LocalChunkService struct {
    baseDir    string
    replicator PosixChunkReplicator
}

func (s *LocalChunkService) WriteChunk(ctx context.Context, id string, offset int64, data []byte) error {
    // 1. Delegate to Replicator
    // The replicator is responsible for ensuring this data reaches the right nodes.
    return s.replicator.Replicate(ctx, id, offset, data)
}

func (s *LocalChunkService) WriteLocal(ctx context.Context, id string, offset int64, data []byte) error {
    // 1. Compute path
    path := filepath.Join(s.baseDir, id)
    
    // 2. Open file (Read/Write mode)
    f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil { return err }
    defer f.Close()
    
    // 3. Seek and Write
    if _, err := f.WriteAt(data, offset); err != nil {
        return err
    }
    return nil
}
```

### Stack

- **Language**: Go
- **Storage**: Standard `os` package (file handling).
- **IO**: Use `io.SectionReader` for efficient reads and `WriteAt` for random writes.
