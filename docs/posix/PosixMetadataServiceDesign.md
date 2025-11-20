# Feature: POSIX Metadata Service Design

## Process

- [ ] **Goals** → Define the "Database" responsibilities.
- [ ] **User stories** → "Atomic update" and "Fast lookup" stories.
- [ ] **Data models** → The schema for Inodes, Dentries, and Superblocks.
- [ ] **Nail an MVP** → CRUD operations and Transaction application.
- [ ] **Draw the prototype** → The relationship between Inodes and the Chunk Map.
- [ ] **Future** → Namespace sharding, BLOB support, ACLs.
- [ ] **Drill into specific components** → Transaction interfaces.
- [ ] **Pick a stack** → Go + In-Memory (for MVP) / KV-Store (Future).
- [ ] **Actual software development**.

## Goals

### Vision and Purpose

The PosixMetadataService maintains the file system structure (the directory tree) and file metadata (attributes + chunk locations).

It acts as the State Machine for the underlying Consensus algorithm (Raft). It takes a stream of "Operations" (Logs) and applies them to its internal maps to produce the current state.

### Functional Goals

- **Strong Consistency**: Reads must reflect the latest applied state.
- **Atomicity**: Support executing a batch of operations (e.g., "Create Inode" + "Link Dentry") as a single atomic unit.
- **Idempotency**: Applying the same operation twice (due to network retries) should not corrupt the state.

### Non-Functional Goals

- **Fast Lookups**: Directory traversal (/a/b/c) happens frequently; inode resolution must be fast (O(1) or O(log n)).
- **Isolation**: It should not know about "Networking" or "File Content." It only knows about IDs, Attributes, and Maps.

## User Stories (State & Consistency Focus)

- **Inode Resolution**: As the FileService, I want to ask for Inode #42 and receive its Type, Size, and Chunk List instantly, so I can serve Read requests.
- **Path Traversal**: As the FileService, I want to provide a parent InodeID and a filename ("config.txt") and get back the child InodeID, so I can walk the directory tree.
- **Atomic Transactions**: As the FileService, I want to submit a batch of changes: `[OpAllocInode(ID=5), OpLinkDentry(Parent=1, Name="file", Child=5)]`. The service must apply all or nothing, ensuring the file system never has a "dangling" file that isn't in a directory.
- **Chunk Mapping**: As the FileService, when a file grows, I want to append `{Offset: 4096 -> ChunkID: 99}` to an inode's metadata without rewriting the entire inode, efficiently handling large files.

## Data Models

This is the "Schema" of your file system.

### 1. Superblock (The Root)

Global configuration for this FS instance.

```go
type Superblock struct {
    FsID            string
    RootInodeID     uint64
    NextInodeID     uint64 // Counter for ID allocation
    CreatedAt       int64
    ChunkSize       int64  // e.g., 4KB or 64KB
}
```

### 2. Inode (The Metadata)

Represents a file or directory.

```go
type Inode struct {
    ID        uint64
    Type      InodeType // File or Directory
    Size      uint64
    Bytes     uint64    // Actual disk usage (sparse files support)
    
    // POSIX Attributes
    Mode      uint32    // Permissions
    Uid       uint32
    Gid       uint32
    Mtime     int64     // Modification Time
    Ctime     int64     // Change Time
    
    // The Map to Data (Crucial for FileService)
    // Mapping: Logical File Offset -> Chunk ID
    Chunks    map[int64]string 
}
```

### 3. Dentry (The Directory Structure)

Represents the contents of a directory.

```go
// Stored separate from Inode to keep Inodes small
type DentryMap map[string]uint64 // Filename -> ChildInodeID
```

## MVP

The MVP must provide methods to Mutate and Query these models.

### Query API:

- `GetSuperblock()`
- `GetInode(id)`
- `GetDentry(parentID, name) -> childID`
- `ListDentry(parentID) -> Iterator<Name, ChildID>`

### Mutation API (Transactions):

The MVP won't expose granular "SetSize" methods. Instead, it exposes an `Execute` method that takes a list of Operations.

- `OpType_CreateInode`
- `OpType_UpdateInode` (Size, Mode, Timestamps)
- `OpType_DeleteInode`
- `OpType_LinkDentry` (Add filename -> inode mapping)
- `OpType_UnlinkDentry` (Remove filename)
- `OpType_AddChunk` (Append chunk to file)

## Prototyping (Transaction Flow)

The distinction between Logic (FileService) and State (MetadataService) is best seen in a transaction.

### Scenario: Create File "/home/docs.txt"

**FileService Logic:**

1.  Check if "docs.txt" exists in "/home" (Read MetadataService).
2.  If not, generate a new ID (Read MetadataService).
3.  Create a Transaction Object.

**The Transaction:**

```json
[
  { "Op": "CreateInode", "ID": 102, "Type": "File", "Mode": 0644 },
  { "Op": "LinkDentry", "ParentID": 50, "Name": "docs.txt", "ChildID": 102 },
  { "Op": "UpdateSuperblock", "IncrementInodeCount": true }
]
```

**MetadataService Execution:**

1.  Lock (Global or Row-level).
2.  Validate: Is ParentID 50 valid? Is Name collision?
3.  Apply: Update Inode Map, Update Dentry Map, Update Superblock.
4.  Unlock.

**Note**: In the full system, this transaction is first sent to Raft (Replicator), which agrees on the order, and THEN applies it to the MetadataService.

## Project Future

### Future Extensions

- **Persistent Storage (RocksDB/BoltDB)**: The MVP will be In-Memory (Go Maps). Moving to disk requires replacing the internal maps with a KV store.
- **Fine-grained Locking**: The MVP can use a single `sync.RWMutex` for the whole state. Future versions should lock per-Inode to allow high concurrency.
- **Snapshots**: To support Raft Log compaction, the service must be able to serialize its entire state (All Inodes + Dentries) to a file efficiently.

## Specific Components

### 1. Interface Definition

`sandstore/internal/posix_metadata/metadata_service.go`

```go
package posixmetadata

import "context"

// PosixMetadataService defines the read/write operations on the FS state.
type PosixMetadataService interface {
    // Reads (Fast, Local)
    GetSuperblock(ctx context.Context) (*Superblock, error)
    GetInode(ctx context.Context, id uint64) (*Inode, error)
    ResolvePath(ctx context.Context, parentID uint64, name string) (uint64, error)
    ListDir(ctx context.Context, inodeID uint64) (map[string]uint64, error)

    // Mutation
    // Execute applies a set of operations atomically.
    // In a Raft setup, this might block until consensus is reached.
    Execute(ctx context.Context, ops []Operation) error
}

// Operation represents a single state change (Command Pattern)
type Operation struct {
    Type OpType
    // Payload fields (using a clean struct or protobuf oneof)
    InodeOp  *InodeOpData
    DentryOp *DentryOpData
}
```

### 2. Implementation (In-Memory)

`sandstore/internal/posix_metadata/memory/service.go`

```go
package memory

import "sync"

type inMemoryMetadata struct {
    mu          sync.RWMutex
    superblock  *Superblock
    inodes      map[uint64]*Inode
    dentries    map[uint64]map[string]uint64 // ParentID -> {Name -> ChildID}
    
    // Replicator acts as the WAL (Write Ahead Log)
    replicator  PosixMetadataReplicator 
}

func (s *inMemoryMetadata) Execute(ctx context.Context, ops []Operation) error {
    // 1. Send to Consensus (Raft)
    // The Replicator will ensure this batch is committed to the log.
    // Once committed, the Replicator calls back into *our* Apply() method.
    return s.replicator.Replicate(ctx, ops)
}

// Apply is called by the Replicator after consensus
func (s *inMemoryMetadata) Apply(ops []Operation) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    for _, op := range ops {
        switch op.Type {
        case OpCreateInode:
            s.inodes[op.InodeOp.ID] = op.InodeOp.Data
        case OpLinkDentry:
            dir := s.dentries[op.DentryOp.ParentID]
            if dir == nil {
                dir = make(map[string]uint64)
                s.dentries[op.DentryOp.ParentID] = dir
            }
            dir[op.DentryOp.Name] = op.DentryOp.ChildID
        // ...
        }
    }
}
```

### 3. Logic: Path Resolution

Helper logic to keep user stories satisfied.

```go
func (s *inMemoryMetadata) ResolvePath(ctx context.Context, parentID uint64, name string) (uint64, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    dirMap, exists := s.dentries[parentID]
    if !exists {
        return 0, ErrDirNotFound
    }
    
    childID, ok := dirMap[name]
    if !ok {
        return 0, ErrFileNotFound
    }
    
    return childID, nil
}
```

## Stack

- **Language**: Go
- **Storage (MVP)**: Native Go Maps (`map[uint64]*Inode`).
- **Concurrency**: `sync.RWMutex` (Global lock for MVP).
- **Serialization**: The `Operation` structs must be serializable (Protobuf) so the Replicator can send them over the wire.
