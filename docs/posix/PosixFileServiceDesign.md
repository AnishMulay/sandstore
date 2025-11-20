# Feature: POSIX File Service Design

## Process

- [ ] **Goals** → Define the core logic responsibilities (The Brain).
- [ ] **User stories** → Logic-centric stories (data splitting, atomicity coordination).
- [ ] **Data models** → Internal models for attributes and file handles.
- [ ] **Nail an MVP** → The implementation of the core 15 POSIX methods.
- [ ] **Draw the prototype** → Visualizing the Read/Write split logic.
- [ ] **Future** → Client caching, Async write-back, Snapshots.
- [ ] **Drill into specific components** → Algorithms for chunk math and transaction building.
- [ ] **Pick a stack** → Go.
- [ ] **Actual software development**.

## Goals

### Vision and Purpose

The `PosixFileService` is the logic layer. It sits between the external API (`Server`) and the internal persistence layers (`Metadata` & `Chunks`).

It ensures data integrity by enforcing the order of operations: "Metadata update first, then data? Or data first?" (It handles the consistency models).

### Functional Goals

-   **Orchestration**: Coordinate atomic metadata operations with potentially non-atomic chunk operations.
-   **Abstraction**: Hide the concept of "chunks" from the upper layers. The `Server` just sees a byte stream; the `FileService` handles the splitting/joining of chunks.
-   **Stateless Logic**: The service itself holds no persistent state. It relies entirely on the `PosixMetadataService` and `PosixChunkService`.

### Non-Functional Goals

-   **Maintainability**: Complex logic (like calculating which chunks overlap a write range) must be isolated in pure helper functions, testable without mocks.
-   **Concurrency Safety**: It must handle parallel operations on the same file safely (though strict locking might happen at the inode level in the `Metadata` service).

## User Stories (Logic Focus)

-   **Chunk Calculation**: As the `FileService`, when I receive a `Read(offset=5000, length=10000)` request and the chunk size is 4KB, I want to mathematically determine exactly which chunk IDs to fetch and what byte ranges to slice from them, so I don't fetch unnecessary data.
-   **Write Coordination**: As the `FileService`, when performing a `Write`, I want to send data to the `ChunkService` before updating the file size in `MetadataService`, so that we don't expose uninitialized data if the write crashes.
-   **Atomic Creation**: As the `FileService`, when `Create` is called, I want to construct a single atomic transaction (add inode + add dentry) and submit it, so that we never end up with an orphaned inode or a dangling directory entry.
-   **Path Resolution**: As the `FileService`, when `Lookup("/a/b/c")` is called, I want to iteratively resolve the path components against the `MetadataService` to find the final inode.

## Data Models

While the Service uses Inodes/Dentries from the Metadata layer, it needs its own "Exchange Objects" to keep the layers clean.

### Core Structs

```go
// Attributes represents the standard POSIX file attributes
// explicitly decoupled from the internal "Inode" struct.
type Attributes struct {
    InodeId   uint64
    Size      uint64
    Mode      uint32
    Mtime     int64
    // ...
}

// Entry is returned by directory listings
type Entry struct {
    InodeId uint64
    Name    string
    Type    FileType // Enum: File, Directory
    Attrs   *Attributes // Optional (for ReadDirPlus)
}

// FSStat represents filesystem health/usage
type FSStat struct {
    TotalBytes uint64
    UsedBytes  uint64
    TotalInodes uint64
}
```

## MVP

The MVP implements the logic for the Standard 15:

-   `GetAttr(inode)`
-   `SetAttr(inode, updates)`
-   `Lookup(parent, name) -> inode`
-   `Access(inode, mask)`
-   `Read(inode, offset, count) -> []byte` (Complex Logic)
-   `Write(inode, offset, data) -> count` (Complex Logic)
-   `Create(parent, name, mode) -> inode` (Atomic Transaction)
-   `Mkdir(parent, name, mode) -> inode` (Atomic Transaction)
-   `Remove(parent, name)` (Atomic Transaction)
-   `Rmdir(parent, name)` (Atomic Transaction)
-   `Rename(oldParent, oldName, newParent, newName)` (Atomic Transaction)
-   `ReadDir(inode, cookie, count)`
-   `ReadDirPlus(inode, cookie, count)`
-   `FsStat()`
-   `FsInfo()`

## Prototyping (Logic Flows)

### The Read/Write Chunking Logic

This is the most critical algorithmic part of the `FileService`.

**Scenario**: Chunk Size = 4KB (4096 bytes). Request: Write 5000 bytes at offset 2048.

**Logic Flow**:

-   **Start Offset**: 2048 (Inside Chunk 0).
-   **End Offset**: 7048 (Inside Chunk 1).

**Calculation**:

-   **Chunk 0**: Write from index 2048 to 4096 (2048 bytes).
-   **Chunk 1**: Write from index 0 to 2952 (2952 bytes).

**Execution**:

-   `ChunkService.Write(chunkId_0, offset=2048, data=part1)`
-   `ChunkService.Write(chunkId_1, offset=0, data=part2)`

### The Atomic Create Transaction

To ensure metadata consistency, `Create` cannot be two separate calls.

**Flow**:

1.  **Prepare**: `inodeID = MetadataService.AllocateID()`
2.  **Prepare**: `inode = NewInode(inodeID, ...)`
3.  **Transaction Build**:
    -   `Op1: PutInode(inode)`
    -   `Op2: LinkDentry(parentInodeID, filename, inodeID)`
4.  **Commit**: `MetadataReplicator.Apply(Transaction{Op1, Op2})`

## Project Future

### Future Extensions

-   **Copy-on-Write (CoW) Snapshots**: The `FileService` logic for `Write` will change to allocate new chunk IDs instead of overwriting old ones if a snapshot exists.
-   **Client Caching (Delegations)**: The `FileService` will track which clients have "leases" on files and issue callbacks (recalls) before allowing a conflicting write.
-   **Striping**: Currently we assume linear chunking (Chunk 0, Chunk 1). Future logic could stripe chunks across different placement groups (RAID-0 style) for bandwidth.

## Specific Components

### 1. Interface Definition

`sandstore/internal/posix_fs/posix_file_service.go`

```go
package posixfs

import (
    "context"
    "sandstore/internal/posix_metadata"
    // ...
)

type PosixFileService interface {
    // Metadata Ops
    GetAttr(ctx context.Context, inodeID uint64) (*Attributes, error)
    SetAttr(ctx context.Context, inodeID uint64, attr *SetAttrArgs) (*Attributes, error)
    Lookup(ctx context.Context, parentInodeID uint64, name string) (uint64, error)
    
    // Data Ops
    Read(ctx context.Context, inodeID uint64, offset int64, length int32) ([]byte, error)
    Write(ctx context.Context, inodeID uint64, offset int64, data []byte) (int32, error)
    
    // Directory Ops
    Create(ctx context.Context, parentInodeID uint64, name string, mode uint32) (uint64, error)
    Mkdir(ctx context.Context, parentInodeID uint64, name string, mode uint32) (uint64, error)
    ReadDir(ctx context.Context, inodeID uint64, cookie uint64, count int32) ([]Entry, uint64, bool, error)
    
    // ... other ops
}
```

### 2. Implementation & Dependencies

`sandstore/internal/posix_fs/simple/file_service.go`

```go
type fileService struct {
    metadataService    posix_metadata.PosixMetadataService
    metadataReplicator posix_metadata.PosixMetadataReplicator
    chunkService       posix_chunk.PosixChunkService
    
    // Config
    chunkSize int64 // e.g., 64KB
}
```

### 3. Algorithm: Write Implementation

This demonstrates the complexity handling.

```go
func (fs *fileService) Write(ctx context.Context, inodeID uint64, offset int64, data []byte) (int32, error) {
    // 1. Fetch Metadata (need current size and chunk list)
    inode, err := fs.metadataService.GetInode(ctx, inodeID)
    if err != nil { return 0, err }
    
    // 2. Calculate Splits
    writes := CalculateChunkWrites(offset, data, fs.chunkSize)
    
    // 3. Execute Chunk Writes (Parallelizable)
    // Note: If the file is sparse or extending, we might need to allocate new chunk IDs here.
    for _, w := range writes {
        chunkID := inode.GetOrAllocateChunkID(w.ChunkIndex) 
        err := fs.chunkService.WriteChunk(ctx, chunkID, w.Offset, w.Data)
        if err != nil { return 0, err }
    }
    
    // 4. Update Metadata (Size/Mtime)
    newSize := max(inode.Size, offset + int64(len(data)))
    fs.metadataService.UpdateInode(ctx, inodeID, func(i *Inode) {
        i.Size = newSize
        i.Mtime = time.Now()
    })
    
    return int32(len(data)), nil
}
```

## Stack

-   **Language**: Go
-   **Dependencies**:
    -   `PosixMetadataService` (Interface)
    -   `PosixChunkService` (Interface)
    -   `PosixMetadataReplicator` (Interface - for atomic transactions)
-   **Testing**: Unit tests for the `CalculateChunkWrites` logic are crucial and should be done purely (no mocks needed).
