# Chunk Refactor

**Project Overview**
• **System:** Sandstore, a distributed file system written in Go.
• **Current State:** The system currently utilizes a tightly coupled, hyperconverged architecture heavily influenced by CockroachDB. The control plane (file system metadata) and data plane (physical data chunks) are virtually indistinguishable in the execution path.

• **Core Issues to Resolve:**
    ◦ **Scatter Search:** The system relies on logical string identifiers for chunks, forcing an inefficient broadcast/peer-probing pattern to locate data during reads.

    ◦ **Orphaned Chunks:** Write operations follow a dangerous, non-transactional sequence; if a failure occurs after writing physical bytes but before the metadata commits, the chunk is permanently orphaned, causing unbounded storage leakage over time.

**Phase 1 Execution Scope (Strict Boundaries)**
• **Logical Decoupling:** Implement the Strategy Design Pattern via strict Go interface contracts to separate the control plane (which manages data about the data) from the data plane (which manages the data itself). Core interfaces to define: `PlacementStrategy`, `ReplicationCoordinator`, `MetadataService`, and `ChunkService`.

• **Two-Phase Commit (2PC):** Introduce a 2PC Write Intent log to establish a transaction boundary between the data plane mutation and the control plane update, guaranteeing atomic writes and eliminating orphaned chunks.

• **CockroachDB Baseline Topology:** Implement a control baseline where the `MetadataService` and `ChunkService` remain physically hyperconverged (running in the same daemon/process, backed by the existing Raft state machine) but are strictly logically separated by the new interfaces.

**Strict Phase 1 Exclusions**
• No physical network separation (e.g., no gRPC or separate remote daemons).
• No advanced external research topologies (GFS pipelining, CRAQ chains, Ceph CRUSH, Dynamo quorums).
• No active background Garbage Collection daemon for sweeping stale intents (intents will be logged, but the active sweeper is deferred to Phase 2).
**Target Study Areas for AI Session**
• **Distributed Systems:** Mechanics of Two-Phase Commit (2PC) protocols, Write-Ahead Logging (WAL) in distributed storage, and atomicity across separated planes.
• **Go Architecture:** Utilizing interface-driven design and dependency injection to achieve clean logical decoupling and pluggable routing behaviors.
• **State Machine Management:** Strategies for mapping distinct logical boundaries (metadata vs. chunk data) onto a single, shared physical Raft consensus log without re-coupling the domains.

---

# Process

- [x]  Goals → list out the goals of the project.
- [x]  User stories → user centric design, 10 to 20 user stories for the project.
- [x]  Data models → define the data models and how the data models are going to be interacting with each other.
- [x]  Drill into the specific components.
- [x]  Codebase impact
- [x]  How will the changes be tested?
- [x]  Pick a stack → programming language, frameworks etc.

---

# Goals

The objective of Phase 1 is strictly limited to the **logical decoupling** of the control plane and data plane within a **physically hyperconverged** architecture, and the introduction of a **Two-Phase Commit (2PC) Write Intent Log** for single-chunk operations.

Success is defined by fulfilling the following boolean criteria:

- **Logical Decoupling via Interfaces:** The codebase must route all chunk and metadata operations through strictly defined Go interfaces (`PlacementStrategy`, `MetadataService`, `ChunkService`, `ReplicationCoordinator`). Direct struct-to-struct calls bypassing these interfaces are prohibited.
- **Eliminate Orphaned Chunks (Atomic Single-Chunk Writes):** A write operation must use a 2PC protocol. Physical bytes are written to a temporary file (`PREPARED` state). The metadata mapping and the intent to commit are written atomically to the Raft log. The temporary file is only renamed to a final chunk file upon observing a `COMMITTED` intent.
- **Eliminate Scatter Search (Deterministic Reads):** Reads must be `O(1)` routing. The system will look up the chunk's exact replica locations via the `MetadataService`. If a replica holds a `PREPARED` temporary file, it must lazily finalize it by checking the Raft-backed Intent Log before serving the read.
- **Unified State Machine Application:** Both the `MetadataService` and the `WriteIntentLog` must be backed by the same physical Raft consensus group. A single Raft `Apply()` must be able to atomically update both domains within a single underlying `bbolt` database transaction.

### 1.5 Non-Goals (Strict AI Exclusions)

To prevent scope creep and architectural hallucinations, the following are explicitly **out of scope** for Phase 1:

- **NO Physical Separation:** Do not implement gRPC, REST, or separate network daemons for the Chunk Service and Metadata Service. They remain in the same Go process, communicating via interface method calls.
- **NO Multi-Chunk Atomicity:** The 2PC protocol guarantees the atomic commit of a *single* chunk. Coordinating transactions that span multiple chunks or files (all-or-nothing file commits) is out of scope.
- **NO Active Garbage Collection Daemon:** Do not implement background tickers or sweeper goroutines to delete `ABORTED` or expired `PREPARED` temporary files. Phase 1 only requires the temporary files to exist and be resolvable on-demand. Active cleanup is deferred to Phase 2.
- **NO Advanced Topologies:** Do not implement GFS pipelining, Ceph CRUSH maps, or external NameNodes. The baseline topology remains identical to the current CockroachDB-style hyperconverged setup.

---

# User Stories

- As a calling application - when I write a large file - I want the POSIX client library to split the file into chunks and orchestrate a two phase commit for each chunk with the backend server - so that the server itself remains simple and only handles single chunk atomicity.
- As a POSIX client library - when I send a single chunk write request - I want the backend server to durably stage the file and log the commit intent before acknowledging success - so that network partitions or crashes never result in orphaned physical bytes on the storage nodes.
- As a POSIX client library - when I request to read a specific chunk - I want the backend server to deterministically route my request to the exact physical nodes using a direct metadata lookup - so that the cluster avoids inefficient broadcast scatter searches.
- As a POSIX client library - when I attempt to read a newly written chunk that a node has staged but not yet finalized - I want the node to lazily finalize the file by verifying its committed status against the Raft log before serving the bytes - so that I am guaranteed strictly consistent reads without waiting for background tasks.
- As a cluster administrator - when I restart a storage node that crashed mid write - I want the node to independently query the unified Raft state machine to determine the fate of its temporary chunk files - so that it can safely apply or discard partial writes without requiring manual intervention.
- As a backend write coordinator - when I fail to receive a successful prepare response from all chunk nodes within a timeout - I want to immediately abort the transaction and return an error to the client - so that I do not commit a chunk that lacks sufficient physical replication.
- As a storage node receiving a prepare request - when I receive a request for a transaction ID that I have already durably staged - I want to validate the checksum and return success without rewriting the file - so that network retries from the coordinator do not cause duplicate physical writes or throw false errors.
- As a storage node serving a read - when I find a temporary chunk file but the Raft intent log states the transaction was aborted or is completely unknown - I want to immediately delete the temporary file and return a not found error - so that I passively clean up failed writes without needing a background garbage collector.
- As a backend write coordinator - when I successfully gather a prepare quorum but the subsequent Raft commit proposal fails or times out - I want to return an error to the client - so that the client knows the write failed even though physical bytes were staged on disk.

---

# Data Models

### Unified Raft Command Envelope

```jsx
// To ensure atomic updates to both the Intent Log and Metadata, the Raft 
// State Machine decodes a unified envelope.
type PayloadType int
const (
	PayloadPosixMeta PayloadType = 1
	PayloadChunkIntent PayloadType = 2
)

type RaftCommandEnvelope struct {
	Type        PayloadType
	PosixMeta   *MetadataOperation      // Existing POSIX metadata operations
	ChunkIntent *ChunkIntentOperation   // New 2PC Intent operations
}

// ChunkIntentOperation replaces ChunkCommitOperation to allow explicit Aborts.
// Proposed to Raft ONLY by the ReplicationCoordinator.
type ChunkIntentOperation struct {
	TxnID     string
	ChunkID   string
	NodeIDs   []string      // The physical placement mapping
	State     IntentState   // Usually StateCommitted, rarely StateAborted
	Timestamp int64
}
```

### Write Intent State Machine

```jsx
// Stored in the shared Raft bbolt database.

type IntentState int
const (
	StateUnknown   IntentState = 0
	StatePrepared  IntentState = 1 // Rarely stored in Raft, mostly implied by disk
	StateCommitted IntentState = 2
	StateAborted   IntentState = 3
)
```

### Control Plane: MetadataService extensions

```jsx
// Extended to track chunk locations and intent states, eliminating scatter search.

type MetadataService interface {
	// ... existing methods ...
	
	// Phase 1 additions:
	UpdateChunkPlacement(ctx context.Context, chunkID string, nodeIDs []string) error
	GetChunkPlacement(ctx context.Context, chunkID string) ([]string, error)
	GetIntentState(ctx context.Context, txnID string) (IntentState, error)
	SetIntentState(ctx context.Context, txnID string, state IntentState) error
}
```

### Data Plane: Chunk Service Interface Refactor

```jsx
// Purely local disk operations. Strictly forbidden from talking to Raft.

type ChunkService interface {
	PrepareChunk(ctx context.Context, txnID string, chunkID string, data []byte, checksum string) error
	CommitChunk(ctx context.Context, txnID string, chunkID string) error
	AbortChunk(ctx context.Context, txnID string, chunkID string) error
	ReadChunk(ctx context.Context, chunkID string) ([]byte, error)
	DeleteChunkLocal(ctx context.Context, chunkID string) error
}
```

### Orchestration: Replication Coordinator

```jsx
// Replaces the naive ChunkReplicator. Orchestrates the 2PC RPCs and Raft proposals.

type ReplicationCoordinator interface {
	WriteChunk(ctx context.Context, chunkID string, data []byte) error
}
```

### Component Internal Data Models (stricly local)

```jsx
// Kept internally by a ChunkService instance to track its own unfinalized files.
// DOES NOT GO INTO RAFT.

type PreparedIndexEntry struct {
	TxnID        string
	ChunkID      string
	TempFilePath string
	CreatedAt    int64
}
```

---

# Component Flows

### Flow 1: The Coordinator Write Flow

```jsx
func (s *TransactionalFileService) Write(ctx context.Context, inodeID string, offset int64, data []byte) (int64, error) {
    // 1. Single Chunk Guard
    if !s.isWithinChunkBoundary(offset, len(data)) {
        return 0, ErrCrossChunkWrite // Client must handle splitting
    }

    // 2. Identify Target
    inode, _ := s.ms.GetInode(ctx, inodeID)
    chunkIdx := offset / s.chunkSize
    
    var chunkID string
    isNewChunk := int(chunkIdx) >= len(inode.ChunkList)
    if isNewChunk {
        chunkID = uuid.New().String()
    } else {
        chunkID = inode.ChunkList[chunkIdx]
    }

    // 3. Placement Strategy
    // Uses ClusterService/Node aliases to find 3 healthy targets
    targetNodes, _ := s.placement.SelectNodes(3) 

    // 4. Data Preparation (Read-Modify-Write)
    // If partial write, read existing chunk and patch it locally
    finalData := s.prepareFullChunkBuffer(ctx, chunkID, offset, data, isNewChunk)
    checksum := s.calculateChecksum(finalData)
    txnID := uuid.New().String()

    // 5. PHASE 1: PREPARE (Parallel RPCs)
    // Start Timer. Must be 100% UNANIMOUS.
    err := s.broadcastPrepare(ctx, targetNodes, txnID, chunkID, finalData, checksum)
    if err != nil {
        // Any failure = Total Abort. 
        // We don't log to Raft. Client retries.
        return 0, fmt.Errorf("prepare phase failed: %w", err)
    }

    // 6. PHASE 2: COMMIT (Atomic Raft Entry)
    envelope := &RaftCommandEnvelope{
        Type: PayloadChunkIntent,
        ChunkIntent: &ChunkIntentOperation{
            TxnID:     txnID,
            ChunkID:   chunkID,
            NodeIDs:   targetNodes,
            State:     StateCommitted,
            Timestamp: time.Now().UnixNano(),
        },
    }

    // Bundle Metadata change only if it's a new chunk or file size changes
    if isNewChunk || (offset + int64(len(data)) > inode.FileSize) {
        envelope.PosixMeta = s.buildMetadataUpdate(inode, chunkID, offset, len(data))
    }

    // Block until Raft applies this to the local state machine
    err = s.raft.Replicate(ctx, envelope) 
    if err != nil {
        return 0, fmt.Errorf("consensus commit failed: %w", err)
    }

    // 7. Response
    // We do NOT wait for physical rename. Lazy finalization handles reads.
    return int64(len(data)), nil
}
```

### Flow 2: The Data Plane Prepare Handler

```jsx
func (cs *LocalDiscChunkService) PrepareChunk(ctx context.Context, txnID string, chunkID string, data []byte, expectedChecksum string) error {
	// 1. Deterministic Naming
	tempFileName := fmt.Sprintf("%s_%s.tmp", chunkID, txnID)
	tempFilePath := filepath.Join(cs.baseDir, tempFileName)

	// 2. Idempotency Check (from your brain dump)
	if _, err := os.Stat(tempFilePath); err == nil {
		// File exists! Let's verify it.
		existingData, readErr := os.ReadFile(tempFilePath)
		if readErr == nil {
			if len(existingData) == len(data) && cs.calculateChecksum(existingData) == expectedChecksum {
				// Perfect match. Duplicate RPC. 
				// Ensure it's in the index and return success immediately.
				cs.addToPreparedIndex(txnID, chunkID, tempFilePath)
				return nil
			}
		}
		// If length/checksum fail, or we couldn't read it, it's a corrupted partial write.
		// We fall through to overwrite it.
	}

	// 3. Write the Physical Bytes (with TRUNC to wipe corrupted data)
	f, err := os.OpenFile(tempFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}
	// Ensure we close the file
	defer f.Close()

	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write chunk data: %w", err)
	}

	// 4. Strict Durability (from your brain dump)
	err = f.Sync()
	if err != nil {
		return fmt.Errorf("failed to fsync temp file: %w", err)
	}

	// 5. Update the Local Read Index (Eliminates O(N) directory scans)
	cs.addToPreparedIndex(txnID, chunkID, tempFilePath)

	return nil
}

// Helper: addToPreparedIndex just updates a local map or local bbolt bucket
func (cs *LocalDiscChunkService) addToPreparedIndex(txnID, chunkID, tempFilePath string) {
	cs.indexLock.Lock()
	defer cs.indexLock.Unlock()
	cs.preparedIndex[chunkID] = PreparedIndexEntry{
		TxnID:        txnID,
		ChunkID:      chunkID,
		TempFilePath: tempFilePath,
		CreatedAt:    time.Now().UnixNano(),
	}
}
```

### Flow 3: New State Machine Apply Flow

```jsx
// 1. The Demultiplexer (inside BoltMetadataService)
func (s *BoltMetadataService) ApplyTransaction(data []byte) error {
	var envelope RaftCommandEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}

	switch envelope.Type {
	case PayloadPosixMeta:
		// Route to your existing apply switch for standard operations
		return s.applyStandardMetadata(envelope.PosixMeta)

	case PayloadChunkIntent:
		// Execute the atomic, cross-domain 2PC commit
		return s.applyChunkIntentAtomic(envelope.ChunkIntent, envelope.PosixMeta)

	default:
		return fmt.Errorf("unknown envelope type")
	}
}

// 2. The Atomic Bbolt Application
func (s *BoltMetadataService) applyChunkIntentAtomic(intent *ChunkIntentOperation, metaUpdate *MetadataOperation) error {
	// A single bbolt Update transaction guarantees all-or-nothing atomicity locally
	return s.db.Update(func(tx *bbolt.Tx) error {
		
		// --- A. LOGICAL DOMAIN 1: WRITE INTENT LOG ---
		intentsBucket, err := tx.CreateBucketIfNotExists([]byte("chunk_intents"))
		if err != nil { return err }
		
		intentBytes, _ := json.Marshal(intent)
		err = intentsBucket.Put([]byte(intent.TxnID), intentBytes)
		if err != nil { return err }

		// --- B. LOGICAL DOMAIN 2: METADATA & PLACEMENT ---
		// Track physical placement (Eliminates Scatter Search)
		placementBucket, err := tx.CreateBucketIfNotExists([]byte("chunk_placements"))
		if err != nil { return err }
		
		placementBytes, _ := json.Marshal(intent.NodeIDs)
		err = placementBucket.Put([]byte(intent.ChunkID), placementBytes)
		if err != nil { return err }

		// --- C. LOGICAL DOMAIN 3: INODE UPDATES (Optional) ---
		// If the write expanded the file or added a new chunk ID
		if metaUpdate != nil && metaUpdate.Type == OpUpdateInode {
			inodesBucket := tx.Bucket([]byte("inodes"))
			if inodesBucket == nil { return fmt.Errorf("inodes bucket missing") }
			
			// Read existing
			existingBytes := inodesBucket.Get([]byte(metaUpdate.InodeID))
			if existingBytes == nil { return fmt.Errorf("inode not found") }
			
			var inode Inode
			json.Unmarshal(existingBytes, &inode)

			// Apply updates
			inode.FileSize = metaUpdate.NewSize
			inode.ChunkList = metaUpdate.NewChunkList
			inode.ModifyTime = time.Now() // Or from metadata update timestamp
			
			// Save back
			updatedBytes, _ := json.Marshal(inode)
			err = inodesBucket.Put([]byte(metaUpdate.InodeID), updatedBytes)
			if err != nil { return err }
		}

		// If we reach here, returning nil commits the bbolt transaction.
		// Atomicity achieved!
		return nil
	})
}
```

### Flow 4: The Deterministic Read and Lazy Finalize Flow

```jsx
// =====================================================================
// PART 1: THE COORDINATOR ROUTING (Inside TransactionalFileService)
// =====================================================================
func (s *TransactionalFileService) Read(ctx context.Context, inodeID string, offset int64, length int64) ([]byte, error) {
	// 1. Logical Lookup
	inode, err := s.ms.GetInode(ctx, inodeID)
	if err != nil { return nil, err }
	
	chunkIdx := offset / s.chunkSize
	if int(chunkIdx) >= len(inode.ChunkList) {
		return nil, fmt.Errorf("EOF: offset beyond file size")
	}
	chunkID := inode.ChunkList[chunkIdx]

	// 2. O(1) Placement Lookup (THE DEATH OF SCATTER SEARCH)
	nodeIDs, err := s.ms.GetChunkPlacement(ctx, chunkID)
	if err != nil || len(nodeIDs) == 0 {
		return nil, fmt.Errorf("chunk placement not found in metadata")
	}

	// 3. Direct Sequential Routing
	for _, nodeID := range nodeIDs {
		// Send standard Read RPC to this specific node
		data, err := s.sendReadRPC(ctx, nodeID, chunkID)
		if err == nil {
			// Fast return on first successful replica
			return data, nil 
		}
	}
	
	return nil, fmt.Errorf("all replicas failed to serve chunk %s", chunkID)
}

// =====================================================================
// PART 2: DATA PLANE HANDLER & LAZY FINALIZE (Inside LocalDiscChunkService)
// =====================================================================
func (cs *LocalDiscChunkService) ReadChunk(ctx context.Context, chunkID string) ([]byte, error) {
	finalFilePath := filepath.Join(cs.baseDir, chunkID)

	// 1. FAST PATH: Chunk is already finalized
	if data, err := os.ReadFile(finalFilePath); err == nil {
		return data, nil
	}

	// 2. SLOW PATH: Check Local Index for a Temp File
	cs.indexLock.Lock()
	entry, exists := cs.preparedIndex[chunkID]
	cs.indexLock.Unlock()

	if !exists {
		return nil, fmt.Errorf("chunk not found locally")
	}

	// 3. Cross-Boundary Check: Query Control Plane for Raft Decision
	state, err := cs.ms.GetIntentState(ctx, entry.TxnID)
	
	// 4. ACT ON INTENT STATE
	if state == StateCommitted {
		// --- LAZY FINALIZE ---
		cs.finalizeLock.Lock() // Prevent concurrent rename panics
		defer cs.finalizeLock.Unlock()
		
		// Double-check it wasn't finalized by a concurrent read while waiting for lock
		if _, err := os.Stat(finalFilePath); os.IsNotExist(err) {
			// ATOMIC RENAME
			if err := os.Rename(entry.TempFilePath, finalFilePath); err != nil {
				return nil, fmt.Errorf("failed to finalize chunk: %w", err)
			}
		}
		
		cs.removeFromPreparedIndex(chunkID)
		return os.ReadFile(finalFilePath)
	}

	if state == StateAborted || (state == StateUnknown && cs.isExpired(entry.CreatedAt)) {
		// --- PASSIVE GARBAGE COLLECTION ---
		os.Remove(entry.TempFilePath)
		cs.removeFromPreparedIndex(chunkID)
		return nil, fmt.Errorf("chunk write was aborted or expired")
	}

	// State is StatePrepared. Transaction is still in flight.
	return nil, fmt.Errorf("chunk is currently locked in an active transaction")
}
```

### Flow 5: Data Plane Lifecycle and Async Handlers

```jsx
// =====================================================================
// PART 1: THE BOOTSTRAP SEQUENCE (Fixes Reboot Amnesia)
// =====================================================================
func (cs *LocalDiscChunkService) Start() error {
	cs.indexLock.Lock()
	defer cs.indexLock.Unlock()

	// Initialize the map if nil
	if cs.preparedIndex == nil {
		cs.preparedIndex = make(map[string]PreparedIndexEntry)
	}

	// Scan the data directory for orphaned .tmp files
	entries, err := os.ReadDir(cs.baseDir)
	if err != nil { return err }

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}

		// Expected format: chunkID_txnID.tmp
		name := strings.TrimSuffix(entry.Name(), ".tmp")
		parts := strings.Split(name, "_")
		if len(parts) != 2 { continue }

		chunkID := parts[0]
		txnID := parts[1]
		
		// Rebuild the in-memory state
		cs.preparedIndex[chunkID] = PreparedIndexEntry{
			TxnID:        txnID,
			ChunkID:      chunkID,
			TempFilePath: filepath.Join(cs.baseDir, entry.Name()),
			CreatedAt:    time.Now().UnixNano(), // Reset TTL clock on boot
		}
	}
	return nil
}

// =====================================================================
// PART 2: ASYNC COMMIT HANDLER
// =====================================================================
func (cs *LocalDiscChunkService) CommitChunk(ctx context.Context, txnID string, chunkID string) error {
	cs.finalizeLock.Lock() // Must share the same lock as Flow 4 (Read Path)
	defer cs.finalizeLock.Unlock()

	// 1. Look up the temp file
	cs.indexLock.Lock()
	entry, exists := cs.preparedIndex[chunkID]
	cs.indexLock.Unlock()

	finalFilePath := filepath.Join(cs.baseDir, chunkID)

	// 2. Idempotency Check: Was it already lazily finalized by a Read?
	if !exists {
		if _, err := os.Stat(finalFilePath); err == nil {
			return nil // Already committed and finalized. Success.
		}
		return fmt.Errorf("transaction not found and chunk not finalized")
	}

	// 3. Verify TxnID matches (prevents ABA problems)
	if entry.TxnID != txnID {
		return fmt.Errorf("txnID mismatch")
	}

	// 4. Atomic Rename (Commits the physical bytes)
	if err := os.Rename(entry.TempFilePath, finalFilePath); err != nil {
		return fmt.Errorf("failed to commit chunk via rename: %w", err)
	}

	// 5. Cleanup memory state
	cs.removeFromPreparedIndex(chunkID)
	return nil
}

// =====================================================================
// PART 3: ASYNC ABORT HANDLER
// =====================================================================
func (cs *LocalDiscChunkService) AbortChunk(ctx context.Context, txnID string, chunkID string) error {
	cs.finalizeLock.Lock()
	defer cs.finalizeLock.Unlock()

	cs.indexLock.Lock()
	entry, exists := cs.preparedIndex[chunkID]
	cs.indexLock.Unlock()

	// 1. Idempotency Check
	if !exists {
		return nil // Already cleaned up (e.g., via passive GC on read)
	}

	if entry.TxnID != txnID {
		return fmt.Errorf("txnID mismatch")
	}

	// 2. Delete the physical temp file
	if err := os.Remove(entry.TempFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete aborted chunk: %w", err)
	}

	// 3. Cleanup memory state
	cs.removeFromPreparedIndex(chunkID)
	return nil
}
```

---

# Codebase impact and blast radius

To achieve zero variability and prevent the AI from rewriting unrelated systems, the modifications for Phase 1 are strictly scoped to the following files and packages. **If a file is not listed in the "Modified" or "New" sections, the AI is strictly forbidden from altering it.**

### 1. New Files (Additive Changes)

- **`internal/file_service/transactional/transactional_file_service.go`**
    - *Reason:* We are creating a brand new implementation of the existing `FileService` interface. This file will house `TransactionalFileService` (Flow 1 and Flow 4 coordinator logic). This ensures the old `SimpleFileService` remains intact for fallback/testing.

### 2. Modified Files (Interface & Struct Definitions)

- **`internal/metadata_service/posix_metadata_service.go`**
    - *Reason:* Needs the new Phase 1 methods appended to the `MetadataService` interface (`UpdateChunkPlacement`, `GetChunkPlacement`, `GetIntentState`, `SetIntentState`).
- **`internal/chunk_service/posix_chunk_service.go`**
    - *Reason:* The `ChunkService` interface must be rewritten to remove the naive `WriteChunkLocal` and replace it with the 2PC methods (`PrepareChunk`, `CommitChunk`, `AbortChunk`).
- **`internal/metadata_replicator/raft_replicator/types.go`** (or equivalent protobuf file)
    - *Reason:* Must be updated to define the `RaftCommandEnvelope`, `PayloadType`, `ChunkIntentOperation`, and `IntentState` enumerations.

### 3. Modified Files (Implementation Updates)

- **`internal/metadata_service/bolt_metadata_service.go`**
    - *Reason:* Must implement the new interface methods. Crucially, the `ApplyTransaction` method must be updated to demultiplex the new `RaftCommandEnvelope` and execute the atomic `bbolt` transaction across the `chunk_intents` and `chunk_placements` buckets (Flow 3).
- **`internal/chunk_service/local_disc_posix_chunk_service.go`**
    - *Reason:* Must implement the new 2PC methods. It will house the `preparedIndex` map, the `Start()` boot sequence, and the lazy-finalization read logic (Flow 2, Flow 4, Flow 5).

### 4. Modified Files (Wiring & Client)

- **`internal/server/simple/simple_posix_server.go` (or `wire.go`)**
    - *Reason:* The dependency injection must be updated to instantiate `TransactionalFileService` instead of `SimpleFileService` and wire it into the RPC handlers.
- **`client/` (POSIX Client Library)**
    - *Reason:* The `Write` implementation in the client must be updated to split large payloads into strictly `chunkSize` boundaries before calling the server's Write RPC.

### 5. Strictly Off-Limits (Do Not Touch)

- **`internal/metadata_replicator/raft_replicator/` (Core Raft Consensus Logic)**
    - *Reason:* We are changing the *payload* of the state machine, but the Raft consensus protocol itself (leader election, log replication, heartbeats) must not be touched.
- **Other Interfaces (`ClusterService`, `LogService`, `Communicator`)**
    - *Reason:* These are infrastructural and out of scope for Phase 1.

---

# Testing and Wiring

The ultimate proof of success for Phase 1 is that the existing `durability_smoke` and `open_smoke` test suites pass **without any modifications to the test files themselves.** To achieve this, the AI must strictly follow these wiring instructions:

### 1. Server Dependency Injection (`servers/node/wire_grpc_etcd.go`)

The AI must replace the instantiation of the `FileService` and wire the new components. It must NOT rewrite the `Server` or `ClusterService` initialization.

**Locate this block in `wire_grpc_etcd.go`:**

Go

	`ms.SetReplicator(metaRepl)
	chunkDir := opts.DataDir + "/chunks/" + opts.NodeID
	cs := chunkservice.NewLocalDiscChunkService(chunkDir, ls, chunkRepl, 2)
	fs := fileservice.NewSimpleFileService(ms, cs, ls)

	// 6. Server (The Gateway)
	srv := simpleserver.NewSimpleServer(comm, fs, cs, ls, metaRepl, chunkRepl)`

**Replace it strictly with:**

Go

	`ms.SetReplicator(metaRepl)
	chunkDir := opts.DataDir + "/chunks/" + opts.NodeID
	
	// cs now implements the 2PC interface (Prepare, Commit, Abort)
	// chunkRepl is no longer used by ChunkService, it's strictly local.
	cs := chunkservice.NewLocalDiscChunkService(chunkDir, ls)
	
	// fs is now the 2PC Coordinator. 
	// It requires the Communicator (for RPCs), Raft (for atomic commits), and ClusterService (for placement)
	chunkSize := int64(8 * 1024 * 1024) // 8MB default
	fs := transactional.NewTransactionalFileService(ms, comm, metaRepl, clusterService, ls, chunkSize)

	// 6. Server (The Gateway)
	// The SimpleServer remains unchanged because fs and cs still satisfy the base interfaces
	srv := simpleserver.NewSimpleServer(comm, fs, cs, ls, metaRepl, chunkRepl)`

### 2. Client Library Updates (`clients/library/client.go`)

To satisfy the "Single Chunk Guard" introduced in Flow 1, the `Write` method inside `sandlib.SandstoreClient` must be modified.

- **The Rule:** The POSIX client library must loop over the user's data payload, slice it into strictly `chunkSize` (8MB) or smaller byte arrays, and send individual `MsgWrite` RPCs to the server for each slice.
- **Constraint:** The client must wait for a successful response from the server for Chunk N before sending the RPC for Chunk N+1.

### 3. Verification Criteria

Once the AI generates the code, the developer will run:

1. `make docker-build` (or equivalent to build the node image).
2. The `durability_smoke` binary.
3. The `open_smoke` binary.

If the tests pass, it guarantees:

- The 2PC Intent Log is durably replicating via Raft.
- The Lazy Finalization read path is serving bytes correctly.
- The strict logical decoupling did not break the existing POSIX API.

---

# Stack

- The entire photo place up till this point of time is in Go, so all these changes and everything will also be in Go.