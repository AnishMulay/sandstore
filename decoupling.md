# Decoupling

# AI Directive & Coding Guidelines

**Role:** You are acting as a strict, Senior Systems Engineer executing a Zero-Variability architectural refactor.
**The Golden Rule:** This Software Design Document (SDDD) is the absolute Single Source of Truth (SSOT). You are forbidden from hallucinating logic, inventing new structs, or modifying the transport/consensus layers unless explicitly directed by this document.

**Rules of Engagement:**

1. **No Out-of-Scope Refactoring:** You are strictly moving the Edge Router decoupling. Do NOT refactor the internal workings of the `DurableRaftReplicator`, the gRPC Communicator, or the Client libraries.
2. **1:1 Metadata Passthrough:** For standard POSIX operations (Mkdir, Create, etc.), you must copy the exact logic from the Phase 1 `transactional_file_service.go` into the new `ControlPlaneOrchestrator`. Do not invent new Raft payloads for these.
3. **Strict Step-by-Step Execution:** You will not write the entire feature at once. You will be prompted to execute the 7-Step Implementation Plan at the bottom of this document, one step at a time. Do not proceed to the next step until the user confirms the current step compiles and passes.

---

# Process

- [x]  Goals → list out the goals of the project.
- [x]  User stories → user centric design, 10 to 20 user stories for the project.
- [x]  Data models → define the data models and how the data models are going to be interacting with each other.
- [x]  New Wiring
- [x]  Drill into the specific components.
- [x]  Codebase impact
- [x]  How will the changes be tested?

---

# Goals

**Architectural Inversion:** 

- Completely deprecate and remove the monolithic FileService interface. Replace it with a stateless ControlPlaneOrchestrator and DataPlaneOrchestrator.
- Topology Agnosticism: Introduce strictly bounded strategy interfaces (PlacementStrategy, TxHandle, EndpointResolver) so the core routing logic has zero knowledge of the underlying consensus protocol (Raft).
- Eradicate Scatter Search: Upgrade the Inode data model to use ChunkLocation structs containing physical endpoints, enabling $O(1)$ direct network routing for reads.
- 100% Regression Safety: Implement concrete Raft-backed versions of these new interfaces (e.g., RaftTxHandle, RaftDataPlaneOrchestrator) that perfectly map to the existing Phase 1 logic. The existing CockroachDB-style test suite must pass without modification.

**Non-Goals (Out of Scope for Phase 2):**

- NO New Topologies: We are not implementing the logic for HDFS, CRAQ, Dynamo, or Ceph in this phase. We are only building the interfaces that will hold them [later.](http://later.no/)
- NO Client-Side Changes: The external client libraries and the gRPC transport layer must remain completely untouched. The refactor boundary stops at the edge server (SimpleServer.handleMessage).
- NO Raft Refactoring: We are moving the Raft consensus calls into TxHandle, but we are not changing how the durable Raft log or the MetadataReplicator actually work.

---

# User Stories

- **The O(1) Read Path:** As the Edge Server, when I receive a `ReadRequest`, I want to ask the Control Plane exactly which physical IP addresses hold the data, so the Data Plane can fetch it directly without broadcasting to the whole cluster.
- **The Stateless Write Path:** As the Edge Server, when I receive a `WriteRequest`, I want to acquire a Two-Phase Commit intent, blindly pass the byte payload to the Data Plane, and commit the intent, so that I don't have to manage Raft consensus loops or wait groups myself.
- **The Metadata Passthrough:** As a Client, when I issue a `Mkdir` or `Create` command, I want the system to route it directly through the Control Plane to the underlying Raft metadata log exactly as it did in Phase 1.
- **The Pluggable Future:** As a Distributed Systems Researcher, I want to be able to write a new `PlacementStrategy` (e.g., a CRUSH algorithm) and inject it into the server wiring without modifying a single line of the file system's POSIX logic.

---

# Data Models

To eradicate the monolithic `FileService`, we split its 17 methods across two distinct orchestrators. The `ControlPlaneOrchestrator` will handle all pure metadata and namespace operations, while acting as the Two-Phase Commit (2PC) intent manager for file data. The `DataPlaneOrchestrator` will strictly handle physical byte movement.

### Domain Primitives

```go
// internal/domain/types.go

type ChunkLocation struct {
	LogicalNodeAlias string 
	PhysicalEndpoint string 
}

// ChunkDescriptor bridges the logical ID to its physical locations for the Inode.
type ChunkDescriptor struct {
	ChunkID   string
	Locations []ChunkLocation
}

type WriteContext struct {
	TxnID        string
	ChunkID      string
	TargetNodes  []ChunkLocation
	IsNewChunk   bool
	FullPayload  []byte // Holds the post-buffered read-modify-write data
}
```

### Strategy and Routing Interfaces

These interfaces abstract away the specific topology. The orchestrators will invoke these interfaces without knowing if the underlying cluster is Raft, Consistent Hashing, or CRUSH.

```go
import "context"
import "github.com/yourrepo/sandstore/internal/domain"
import pms "github.com/yourrepo/sandstore/internal/metadata_service"

// 1. PlacementStrategy determines "WHO" holds the data (Topology Rules).
// Implementations: InMemoryPlacement, RaftLeaseholderPlacement, CRUSHPlacement, ConsistentHashingRing.
type PlacementStrategy interface {
	SelectTargets(ctx context.Context, chunkID string, replicaCount int) ([]domain.ChunkLocation, error)
}

// 2. EndpointResolver determines "WHERE" a logical node currently resides (Transport Layer).
// Critical for surviving network churn and IP changes without altering metadata.
type EndpointResolver interface {
	ResolveEndpoint(ctx context.Context, logicalAlias string) (string, error)
}

// 3. TransactionCoordinator acts as a factory for isolated 2PC state machines.
// Implementations: RaftTransactionCoordinator, StandaloneLogCoordinator.
type TransactionCoordinator interface {
	// Creates a new, isolated state machine per request to prevent cross-request state leakage.
	NewTransaction(txnID string) TxHandle
}

// 4. TxHandle isolates the state of a single request. 
// It is the ONLY interface allowed to propose state changes to the consensus layer (e.g., Raft).
// internal/orchestrators/interfaces.go

type TxHandle interface {
	Init(ctx context.Context, chunkID string, participants []ChunkLocation) error
	
	// Participants must be passed in again because the handle is instantiated statelessly per-request
	Commit(ctx context.Context, metaUpdate *pms.MetadataOperation, participants []ChunkLocation) error
	Abort(ctx context.Context, participants []ChunkLocation) error
}

```

### Decoupled Orchestrators

This layer completely replaces the old `posix_file_service.go` `FileService` interface. These orchestrators hold zero state and merely sequence the flow using the injected strategy interfaces.

```go
import "context"
import "github.com/yourrepo/sandstore/internal/domain"
import pms "github.com/yourrepo/sandstore/internal/metadata_service"

// ControlPlaneOrchestrator manages the namespace, permissions, and 2PC intents.
// Notice that Read() and Write() do NOT accept or return []byte payloads here.
type ControlPlaneOrchestrator interface {
	// --- Lifecycle ---
	Start() error
	Stop() error

	// --- Pure Metadata & Namespace Operations (1:1 Passthrough) ---
	GetAttr(ctx context.Context, inodeID string) (*pms.Attributes, error)
	SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error)
	LookupPath(ctx context.Context, path string) (string, error)
	Lookup(ctx context.Context, parentInodeID string, name string) (string, error)
	Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error
	Create(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)
	Mkdir(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)
	Remove(ctx context.Context, parentID string, name string) error
	Rmdir(ctx context.Context, parentID string, name string) error
	Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error
	ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error)
	ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error)
	GetFsStat(ctx context.Context) (*pms.FileSystemStats, error)
	GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error)

	// --- Phase 1 & 3 of Data Operations ---
	PrepareFileWrite(ctx context.Context, inodeID string, offset int64, length int64) (*domain.WriteContext, error)
	CommitFileWrite(ctx context.Context, txnID string, chunkID string, newEOF int64, isNewChunk bool) error
	AbortFileWrite(ctx context.Context, txnID string) error
	
	PrepareFileRead(ctx context.Context, inodeID string, offset int64) (*domain.ReadContext, error)
}

// DataPlaneOrchestrator strictly moves bytes to the physical locations calculated by the Control Plane.
type DataPlaneOrchestrator interface {
	// Pushes bytes to targets. Implementations: MultiRaftBroadcast, HDFSPipeline, CRAQChain.
	ExecuteWrite(ctx context.Context, txnID string, chunkID string, data []byte, targets []domain.ChunkLocation) error
	
	// Pulls bytes directly from specific targets (O(1) routing).
	ExecuteRead(ctx context.Context, chunkID string, targets []domain.ChunkLocation) ([]byte, error)
}
```

---

# New Wiring

We are modifying `servers/node/wire_grpc_etcd.go` and `internal/server/simple/simple_posix_server.go`. The `SimpleServer` struct drops `fs FileService` and now requires the two orchestrators.

### Server Struct Update

```go
// internal/server/simple/simple_posix_server.go

type SimpleServer struct {
	comm *grpcServer // or your communication interface
	cpo  orchestrators.ControlPlaneOrchestrator
	dpo  orchestrators.DataPlaneOrchestrator
	// ... other dependencies (cs, ls, replicators) remain for now
}
```

### The Metadata Flow (e.g., Mkdir, Create, GetAttr)

For the 15 methods that do nout touch physical chunk data, the server acts as a direct passthrough to the control plane.

```go
// internal/server/simple/simple_posix_server.go

// --- 8. MKDIR ---
case ps.MsgMkdir:
	req := msg.Payload.(ps.MkdirRequest)
	// Direct passthrough. Control plane handles the Raft consensus for the namespace update.
	inode, err := s.cpo.Mkdir(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
	return s.respond(inode, err)

// --- 7. CREATE ---
case ps.MsgCreate:
	req := msg.Payload.(ps.CreateRequest)
	inode, err := s.cpo.Create(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
	return s.respond(inode, err)
```

### The Write Flow (2PC Orchestration)

This is the most critical transformation. The monolithic fs.Write() is broken into the explicit 2 Phase Commit sequence inside the server’s network handler. The server orchestrates the sequence, ensuring the planes remaing decoupled.

```go
// internal/server/simple/simple_posix_server.go

// --- 6. WRITE ---
case ps.MsgWrite:
	req := msg.Payload.(ps.WriteRequest)
	
	// Phase 1: Ask Control Plane to lock the inode, allocate chunk IDs, and determine placement.
	writeCtx, err := s.cpo.PrepareFileWrite(ctx, req.InodeID, req.Offset, int64(len(req.Data)))
	if err != nil {
		return s.respond(0, err)
	}

	// Phase 2: Blindly pass the bytes and the explicit target locations to the Data Plane.
	// The DPO uses the injected ReplicationCoordinator (e.g., MultiRaft, Chain, Quorum).
	err = s.dpo.ExecuteWrite(ctx, writeCtx.TxnID, writeCtx.ChunkID, req.Data, writeCtx.TargetNodes)
	if err != nil {
		// If physical replication fails, we MUST abort the intent in the control plane.
		s.cpo.AbortFileWrite(ctx, writeCtx.TxnID) 
		return s.respond(0, err)
	}

	// Phase 3: Tell Control Plane the physical bytes are durably stored. Commit the intent.
	newEOF := req.Offset + int64(len(req.Data))
	err = s.cpo.CommitFileWrite(ctx, writeCtx.TxnID, writeCtx.ChunkID, newEOF, writeCtx.IsNewChunk)
	
	return s.respond(int64(len(req.Data)), err)
```

### The Read Flow (O(1) Routing)

The read path is transformed to eliminate the “scatter search” bottleneck

```go
// internal/server/simple/simple_posix_server.go

// --- 5. READ ---
case ps.MsgRead:
	req := msg.Payload.(ps.ReadRequest)
	
	// Phase 1: Ask Control Plane exactly which physical nodes hold this offset's chunk.
	readCtx, err := s.cpo.PrepareFileRead(ctx, req.InodeID, req.Offset)
	if err != nil {
		return s.respond(nil, err)
	}

	// Phase 2: Data Plane fetches directly from those specific nodes.
	data, err := s.dpo.ExecuteRead(ctx, readCtx.ChunkID, readCtx.TargetNodes)
	
	return s.respond(data, err)
```

---

# New Component Flows

This section dictates the exact internal implementation of the `ControlPlaneOrchestrator`. It enforces the stateless, in-memory 2PC rules and strictly separates POSIX logic from consensus logic.

### The Write Path: Phase 1 (Prepare)

**Rule:** The orchestrator must remain stateless. `TxHandle.Init` is strictly an in-memory operation (no disk I/O) to maximize performance. For existing chunks, we bypass the Placement Strategy and re-use the existing replicas to avoid Copy-on-Write cleanup complexity.

```go
// internal/orchestrators/control_plane.go

func (c *controlPlaneOrchestrator) PrepareFileWrite(ctx context.Context, inodeID string, offset int64, length int64) (*domain.WriteContext, error) {
	// 1. Fetch current metadata
	inode, err := c.metadataService.GetInode(ctx, inodeID)
	if err != nil {
		return nil, err
	}

	chunkIdx := offset / c.chunkSize
	isNewChunk := int(chunkIdx) >= len(inode.ChunkList)
	
	var chunkID string
	var targets []domain.ChunkLocation

	if isNewChunk {
		// New Data: Generate ID and consult the Topology strategy (e.g., CRUSH or RaftLeaseholder)
		chunkID = uuid.NewString()
		targets, err = c.placementStrategy.SelectTargets(ctx, chunkID, c.replicaCount)
		if err != nil {
			return nil, err
		}
	} else {
		// Existing Data: Pin to existing nodes to prevent orphaned replicas/Copy-On-Write overhead
		chunkID = inode.ChunkList[chunkIdx].ChunkID
		targets = inode.ChunkList[chunkIdx].Locations 
	}

	// 2. Initialize Stateless Request Context
	txnID := uuid.NewString()
	
	// 3. Setup In-Memory Intent (NO DISK I/O HERE)
	handle := c.txnCoordinator.NewTransaction(txnID)
	if err := handle.Init(ctx, chunkID, targets); err != nil {
		return nil, err
	}

	return &domain.WriteContext{
		TxnID:       txnID,
		ChunkID:     chunkID,
		TargetNodes: targets,
		IsNewChunk:  isNewChunk,
	}, nil
}
```

### The Write Path: Phase 3 (Commit)

**Rule:** The orchestrator must dynamically recalculate the metadata payload and push it to the consensus layer. The consensus layer (`TxHandle`) performs the *only* disk I/O in the entire write sequence.

```go
// internal/orchestrators/control_plane.go

// Notice we now pass the target nodes back in so the stateless handle can broadcast
func (c *controlPlaneOrchestrator) CommitFileWrite(ctx context.Context, txnID string, chunkID string, newEOF int64, isNewChunk bool, targets []domain.ChunkLocation) error {
	handle := c.txnCoordinator.NewTransaction(txnID)
	
	// 1. Fetch existing Inode to build the FULL replacement list BoltDB expects
	inode, _ := c.metadataService.GetInode(ctx, inodeID)
	
	newChunkList := make([]domain.ChunkDescriptor, len(inode.ChunkList))
	copy(newChunkList, inode.ChunkList)

	if isNewChunk {
		newChunkList = append(newChunkList, domain.ChunkDescriptor{
			ChunkID:   chunkID,
			Locations: targets,
		})
	}

	metaUpdate := &pms.MetadataOperation{
		Type:         pms.OpUpdateInode,
		NewSize:      &newEOF,
		NewChunkList: newChunkList, // Full replacement list, not append
		Timestamp:    time.Now().UnixNano(),
	}

	return handle.Commit(ctx, metaUpdate, targets)
}
```

### The Read Path (O(1) Direct Routing)

**Rule:** The read path must avoid "scatter search." It uses the pre-calculated `ChunkLocation`s stored inside the `Inode` to tell the Data Plane exactly where to go.

```go
// internal/orchestrators/control_plane.go

func (c *controlPlaneOrchestrator) PrepareFileRead(ctx context.Context, inodeID string, offset int64) (*domain.ReadContext, error) {
	// 1. Fetch exact namespace mapping
	inode, err := c.metadataService.GetInode(ctx, inodeID)
	if err != nil {
		return nil, err
	}

	chunkIdx := offset / c.chunkSize
	if int(chunkIdx) >= len(inode.ChunkList) {
		return nil, errors.New("read past EOF")
	}

	// 2. Extract the topology-aware physical/logical locations
	targetChunk := inode.ChunkList[chunkIdx]

	return &domain.ReadContext{
		ChunkID:     targetChunk.ChunkID,
		TargetNodes: targetChunk.Locations,
	}, nil
}
```

### The POSIX Metadata Passthrough (1:1 Migration)

**Rule:** The `ControlPlaneOrchestrator` assumes full responsibility for all standard POSIX operations that do not involve physical chunk data. The internal business logic, metadata service calls, and Raft metadata proposals for these methods are **strictly unmodified** from their Phase 1 implementations in the old `posix_file_service.go`.

The AI agent is instructed to literally cut these functions from the old `FileService` implementation and paste them into the `ControlPlaneOrchestrator` implementation, only updating the receiver struct.

**Methods included in this strict 1:1 migration:**`GetAttr`, `SetAttr`, `LookupPath`, `Lookup`, `Access`, `Create`, `Mkdir`, `Remove`, `Rmdir`, `Rename`, `ReadDir`, `ReadDirPlus`, `GetFsStat`, `GetFsInfo`.

**Code Example (Strict Adherence Matrix):**

```go
// internal/orchestrators/control_plane.go

// The logic inside this function is IDENTICAL to the old posix_file_service.go Mkdir.
// The AI must NOT alter the internal Raft proposals or metadata service invocations.
func (c *controlPlaneOrchestrator) Mkdir(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	// ... [EXACT EXISTING PHASE 1 LOGIC COPIED HERE] ...
    // e.g., fetching parent inode, checking permissions, creating Raft envelope 
    // for OpMkdir, and calling c.raft.Replicate() or c.metadataService.Mkdir()
}

func (c *controlPlaneOrchestrator) Create(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	// ... [EXACT EXISTING PHASE 1 LOGIC COPIED HERE] ...
}

// ... [Same for the remaining 13 metadata methods] ...
```

### The Data Plane Orchestrator (Physical Byte Movement)

**Rule:** The `RaftDataPlaneOrchestrator` implements `ExecuteWrite` using the exact `broadcastPrepare` logic from Phase 1. It transforms `domain.ChunkLocation` back into the required `communication.Message`.

```go
// internal/orchestrators/raft_data_plane.go

type RaftDataPlaneOrchestrator struct {
	comm communication.Communicator
}

func (d *RaftDataPlaneOrchestrator) ExecuteWrite(ctx context.Context, txnID string, chunkID string, data []byte, targets []domain.ChunkLocation) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(targets))

    // Optional: Calculate checksum if your system requires it here
	checksum := calculateChecksum(data) 

	for _, node := range targets {
		wg.Add(1)
		go func(loc domain.ChunkLocation) {
			defer wg.Done()

			msg := communication.Message{
				From: d.comm.Address(),
				Type: communication.MessageTypePrepareChunk,
				Payload: communication.PrepareChunkRequest{
					TxnID:    txnID,
					ChunkID:  chunkID,
					Data:     data,
					Checksum: checksum,
				},
			}

			// Uses the explicitly resolved PhysicalEndpoint
			resp, err := d.comm.Send(ctx, loc.PhysicalEndpoint, msg)
			if err != nil {
				errCh <- fmt.Errorf("prepare to node %s failed: %w", loc.LogicalNodeAlias, err)
				return
			}
			if resp.Code != communication.CodeOK {
				errCh <- fmt.Errorf("prepare to node %s returned %s", loc.LogicalNodeAlias, resp.Code)
			}
		}(node)
	}

	wg.Wait()
	close(errCh)

	var firstErr error
	failures := 0
	for err := range errCh {
		if firstErr == nil { firstErr = err }
		failures++
	}
	if failures > 0 {
		return fmt.Errorf("%d/%d prepare RPCs failed: %w", failures, len(targets), firstErr)
	}
	return nil
}
```

### The Transaction Coordinator and Handle (Consensus)

**Rule:** The `RaftTxHandle` encapsulates the Two-Phase Commit state. Its `Commit` method is the **only** place where `MetadataReplicator.Replicate` is called. It also assumes responsibility for the async commit/abort broadcasts.

```go
// internal/orchestrators/raft_tx_coordinator.go

type RaftTxHandle struct {
	txnID        string
	chunkID      string
	participants []domain.ChunkLocation
	replicator   metadata_replicator.MetadataReplicator
	comm         communication.Communicator
}

func (h *RaftTxHandle) Init(ctx context.Context, chunkID string, participants []domain.ChunkLocation) error {
	h.chunkID = chunkID
	h.participants = participants
	return nil // Purely in-memory for performance
}

func (h *RaftTxHandle) Commit(ctx context.Context, metaUpdate *pms.MetadataOperation) error {
    // 1. Build the Raft Envelope (IDENTICAL to Phase 1)
	envelope := rr.RaftCommandEnvelope{
		Type: rr.PayloadChunkIntent,
		ChunkIntent: &rr.ChunkIntentOperation{
			TxnID:     h.txnID,
			ChunkID:   h.chunkID,
			NodeIDs:   extractNodeIDsFromLocations(h.participants), // logical aliases
			State:     rr.StateCommitted,
			Timestamp: time.Now().UnixNano(),
		},
		PosixMeta: metaUpdate, 
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal raft envelope: %w", err)
	}
	
	// 2. Perform the Durable Write
	if err := h.replicator.Replicate(ctx, payload); err != nil {
		return fmt.Errorf("consensus commit failed: %w", err)
	}

	// 3. Fire-and-Forget Commit Broadcast to Chunk Nodes (IDENTICAL to Phase 1)
	for _, loc := range h.participants {
		go func(endpoint string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			msg := communication.Message{
				From: h.comm.Address(),
				Type: communication.MessageTypeCommitChunk,
				Payload: communication.CommitChunkRequest{
					TxnID:   h.txnID,
					ChunkID: h.chunkID,
				},
			}
			_, _ = h.comm.Send(ctx, endpoint, msg)
		}(loc.PhysicalEndpoint)
	}

	return nil
}

func (h *RaftTxHandle) Abort(ctx context.Context) error {
	// Fire-and-Forget Abort Broadcast to Chunk Nodes (IDENTICAL to Phase 1)
	for _, loc := range h.participants {
		go func(endpoint string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			msg := communication.Message{
				From: h.comm.Address(),
				Type: communication.MessageTypeAbortChunk,
				Payload: communication.AbortChunkRequest{
					TxnID:   h.txnID,
					ChunkID: h.chunkID,
				},
			}
			_, _ = h.comm.Send(ctx, endpoint, msg)
		}(loc.PhysicalEndpoint)
	}
	return nil
}
```

---

## Codebase Impact & Blast Radius

To ensure regression safety, the blast radius is strictly contained to the edge orchestration layer.

**NEW Files (To be created):**

- `internal/domain/types.go` (For `ChunkLocation`, `ChunkDescriptor`, `WriteContext`, `ReadContext`)
- `internal/orchestrators/interfaces.go` (For `PlacementStrategy`, `TxHandle`, `ControlPlaneOrchestrator`, etc.)
- `internal/orchestrators/control_plane.go` (The brain and POSIX passthrough)
- `internal/orchestrators/raft_data_plane.go` (For network fanout and read-modify-write chunk buffering)
- `internal/orchestrators/raft_tx_coordinator.go` (For Raft transaction isolation)
- `internal/orchestrators/cluster_placement.go` (For `LegacySortedPlacementStrategy`)
- `internal/orchestrators/cluster_endpoint_resolver.go` (For `StaticEndpointResolver`)

**MODIFIED Files (Authorized for changes):**

- `internal/metadata_service/types.go` (Update `Inode.ChunkList` to `[]domain.ChunkDescriptor`)
- `internal/metadata_service/ops.go` (Update metadata payloads for chunk descriptor arrays)
- `internal/metadata_service/posix_metadata_service.go` (Interface updates for new Inode schema)
- `internal/metadata_service/bolt/bolt_metadata_service.go` & `inmemory/inmemory_posix_metadata_service.go` (Update DB serialization for new Inode schema)
- `internal/server/simple/simple_posix_server.go` (Drop `FileService`, wire CPO/DPO)
- `servers/node/wire_grpc_etcd.go` (Update Dependency Injection)

**STRICTLY OFF-LIMITS (Do not touch):**

- `gen/proto/communication/*` (gRPC boundaries must not change)
- `clients/library/*` and CLI smoke clients
- `internal/communication/*` (Wire protocol remains unchanged)
- `internal/metadata_replicator/durable_raft/*` and `raft_replicator/*` (Core consensus mechanics)
- `internal/cluster_service/*`
- `internal/chunk_service/*`
- `internal/file_service/simple/simple_posix_file_service.go` (Read-only reference)

---

## Testing & 7-Step Implementation Plan

**Testing Strategy:** We will use the existing Phase 1 CockroachDB-style test suite. Because we are implementing concrete Raft wrappers (`RaftDataPlaneOrchestrator`, `RaftTxHandle`), the system behavior must remain identical. 100% test pass rate on the existing suite validates the refactor.

**Step-by-Step Execution:**
The coding AI will be prompted to execute these steps sequentially.

- **Step 1: Domain & Strategy Interfaces:** Create `internal/domain/types.go` and `internal/orchestrators/interfaces.go`.
- **Step 2: Concrete Strategy Implementations:** Create `cluster_placement.go` and `cluster_endpoint_resolver.go` mimicking the legacy Phase 1 logic.
- **Step 3: Concrete Data Plane:** Create `raft_data_plane.go`, moving the `broadcastPrepare` and read-modify-write buffering logic into it.
- **Step 4: Concrete Control Plane:** Create `raft_tx_coordinator.go`, housing the Raft envelope creation and async commit/abort logic.
- **Step 5: Control Plane Orchestrator:** Create `control_plane.go`, implementing the 2PC sequences and the 1:1 POSIX passthroughs.
- **Step 6: Metadata Schema Migration:** Update `metadata_service` types, operations, and Bolt/InMemory storage implementations to support `ChunkDescriptor`.
- **Step 7: Server Translation & Wiring:** Update `simple_posix_server.go` handlers and `wire_grpc_etcd.go` to inject the new stack. Run tests.