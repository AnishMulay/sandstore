# SimpleServer Legacy Refactor

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

### Goals:

- **Eradicate Leaky Abstractions:** Completely remove the `metaRepl any` anti-pattern from `SimpleServer`. The edge gateway must not rely on runtime type assertions to route network traffic.
- **Compile-Time Strictness:** Introduce a strongly typed `ConsensusMessageHandler` interface into the `ControlPlaneOrchestrator`, ensuring all dependency injection is validated at compile-time during server wiring.
- **Remove Dead Code:** Cleanly excise the unused `chunkRepl` legacy state from the `SimpleServer` struct and its constructors.
- **Topology Agnosticism (Preparation):** Ensure the `SimpleServer` acts purely as a dumb network host. It must have zero knowledge of Raft or consensus logic, paving the way for stateless Gateway nodes in future topologies.

### Non-Goals (Out of Scope):

- **NO Consensus Refactoring:** We are strictly moving where the RPCs are routed. We are absolutely not changing the internal workings of the `DurableRaftReplicator` or how Raft achieves consensus.
- **NO Transport Layer Changes:** The gRPC message payloads and the core `Communicator` interface remain strictly untouched.
- **NO Client Impact:** Client libraries and smoke test harnesses remain completely unaware of this internal routing change.

---

# User Stories

- The Dumb Gateway: As the `SimpleServer`, when I receive a Raft-specific network message (e.g., `RequestVote`), I want to blindly hand it to the Control Plane Orchestrator without having to cast an `any` interface, so I can remain completely consensus-agnostic.
- The Typed Control Plane: As the `ControlPlaneOrchestrator`, I want an explicit, strongly-typed `ConsensusMessageHandler` injected into my constructor, so I can guarantee at compile-time that I have the necessary dependencies to route peer-to-peer consensus requests.
- The Future Researcher: As a Distributed Systems Architect, I want to be able to wire up a `SimpleServer` without being forced to provide a metadata replicator, so I can easily benchmark "Storage-Only" topologies in the future.

---

# Data Models and Interfaces

To eradicate the `metaRepl any` anti-pattern from `SimpleServer`, we must formalize the Raft RPC contract. We will introduce a new interface specifically for routing consensus messages, and extend the `ControlPlaneOrchestrator` interface to accept these calls, ensuring the Edge Server remains completely agnostic to the underlying consensus protocol.

### The Consensus Message Handler

Create a strict interface to replace `any`. This defines exactly what a consensus engine must support for peer-to-peer routing.

```go
// internal/orchestrators/interfaces.go (Additions)
import (
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator/raft" 
	// Adjust imports based on your actual raft package locations
)

// ConsensusMessageHandler defines the peer-to-peer RPCs required by the consensus layer.
type ConsensusMessageHandler interface {
	HandleRequestVote(req raft.RequestVoteArgs) (*raft.RequestVoteReply, error)
	HandleAppendEntries(req communication.AppendEntriesRequest) (*raft.AppendEntriesReply, error)
	HandleInstallSnapshot(req communication.InstallSnapshotRequest) (*raft.InstallSnapshotReply, error)
}
```

### Control Plane Orchestration Extension

The Control Plane acts as the shield for the server. We add the consensus passthrough methods to its public interface.

```go
// internal/orchestrators/interfaces.go (Modify ControlPlaneOrchestrator)
type ControlPlaneOrchestrator interface {
	// ... [Existing Methods: Start, Stop, GetAttr, PrepareFileWrite, etc.] ...

	// --- Consensus Routing Passthrough ---
	HandleConsensusRequestVote(ctx context.Context, req raft.RequestVoteArgs) (*raft.RequestVoteReply, error)
	HandleConsensusAppendEntries(ctx context.Context, req communication.AppendEntriesRequest) (*raft.AppendEntriesReply, error)
	HandleConsensusInstallSnapshot(ctx context.Context, req communication.InstallSnapshotRequest) (*raft.InstallSnapshotReply, error)
}
```

---

# New Wiring

We are modifying `internal/server/simple/simple_posix_server.go` to drop the legacy fields, and `servers/node/wire_grpc_etcd.go` to redirect the dependency injection.

### Server Struct Update

`SimpleServer` becomes a pure routing host.

```go
// internal/server/simple/simple_posix_server.go
type SimpleServer struct {
	comm *grpccomm.GRPCCommunicator
	cpo  orchestrators.ControlPlaneOrchestrator
	dpo  orchestrators.DataPlaneOrchestrator
	cs   pcs.ChunkService
	ls   log_service.LogService
	// metaRepl (any) and chunkRepl HAVE BEEN REMOVED
}

func NewSimpleServer(
	comm *grpccomm.GRPCCommunicator,
	cpo orchestrators.ControlPlaneOrchestrator,
	dpo orchestrators.DataPlaneOrchestrator,
	cs pcs.ChunkService,
	ls log_service.LogService,
) *SimpleServer {
	return &SimpleServer{
		comm: comm,
		cpo:  cpo,
		dpo:  dpo,
		cs:   cs,
		ls:   ls,
	}
}
```

### Dependency Injection Update

We must type-cast `metaRepl` at the highest possible level (the wiring boundary) to ensure type safety before it enters the orchestrator logic.

```go
// servers/node/wire_grpc_etcd.go (Modification)
	
	// Ensure metaRepl implements our strict interface at compile/wire-time
	consensusHandler, ok := metaRepl.(orchestrators.ConsensusMessageHandler)
	if !ok {
		panic("metaRepl does not implement orchestrators.ConsensusMessageHandler")
	}

	cpo := orchestrators.NewControlPlaneOrchestrator(
		ms, 
		placementStrategy, 
		txnCoordinator, 
		consensusHandler, // INJECT NEW DEPENDENCY HERE
		chunkSize, 
		replicaCount,
	)

	// srv constructor drops metaRepl and chunkRepl completely
	srv := simpleserver.NewSimpleServer(comm, cpo, dpo, cs, ls)
```

---

# Specific Component Flows

This section dictates the exact refactor for the incoming request flow and the Control Plane struct internal updates.

### The Server Passthrough (Delegation)

*Rule:* The `SimpleServer.handleMessage` cases for Raft must execute zero business logic and immediately delegate to the Control Plane Orchestrator. No type assertions are permitted here.

```go
// internal/server/simple/simple_posix_server.go (Inside handleMessage)

case ps.MsgRaftRequestVote:
	req := msg.Payload.(raft.RequestVoteArgs)
	res, err := s.cpo.HandleConsensusRequestVote(ctx, req)
	return s.respond(res, err)

case ps.MsgRaftAppendEntries:
	req := msg.Payload.(communication.AppendEntriesRequest)
	res, err := s.cpo.HandleConsensusAppendEntries(ctx, req)
	return s.respond(res, err)

case ps.MsgRaftInstallSnapshot:
	req := msg.Payload.(communication.InstallSnapshotRequest)
	res, err := s.cpo.HandleConsensusInstallSnapshot(ctx, req)
	return s.respond(res, err)
```

### The Control Plane Orchestration Implementation

*Rule:* The CPO accepts the newly typed `ConsensusMessageHandler` and explicitly wires the delegated requests into the consensus engine.

```go
// internal/orchestrators/control_plane.go

type controlPlaneOrchestrator struct {
	metadataService   pms.MetadataService
	placementStrategy PlacementStrategy
	txnCoordinator    TransactionCoordinator
	consensusHandler  ConsensusMessageHandler // NEW EXPLICIT DEPENDENCY
	chunkSize         int64
	replicaCount      int
}

func NewControlPlaneOrchestrator(
	metadataService pms.MetadataService,
	placementStrategy PlacementStrategy,
	txnCoordinator TransactionCoordinator,
	consensusHandler ConsensusMessageHandler, // NEW PARAMETER
	chunkSize int64,
	replicaCount int,
) *controlPlaneOrchestrator {
	// ... [Existing initialization logic] ...
	return &controlPlaneOrchestrator{
		metadataService:   metadataService,
		placementStrategy: placementStrategy,
		txnCoordinator:    txnCoordinator,
		consensusHandler:  consensusHandler,
		chunkSize:         chunkSize,
		replicaCount:      replicaCount,
	}
}

// --- Consensus Passthrough Implementations ---

func (c *controlPlaneOrchestrator) HandleConsensusRequestVote(ctx context.Context, req raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	if c.consensusHandler == nil {
		return nil, errors.New("consensus handler not configured")
	}
	return c.consensusHandler.HandleRequestVote(req)
}

func (c *controlPlaneOrchestrator) HandleConsensusAppendEntries(ctx context.Context, req communication.AppendEntriesRequest) (*raft.AppendEntriesReply, error) {
	if c.consensusHandler == nil {
		return nil, errors.New("consensus handler not configured")
	}
	return c.consensusHandler.HandleAppendEntries(req)
}

func (c *controlPlaneOrchestrator) HandleConsensusInstallSnapshot(ctx context.Context, req communication.InstallSnapshotRequest) (*raft.InstallSnapshotReply, error) {
	if c.consensusHandler == nil {
		return nil, errors.New("consensus handler not configured")
	}
	return c.consensusHandler.HandleInstallSnapshot(req)
}
```

---

# Codebase Impact and Blast Radius

To ensure regression safety, the blast radius is strictly contained to the edge server and orchestration layer.

**Modified Files (Authorized for changes):**

- `internal/orchestrators/interfaces.go`: Add `ConsensusMessageHandler` (with `ctx context.Context` parameters) and extend `ControlPlaneOrchestrator`.
- `internal/orchestrators/control_plane.go`: Add `consensusHandler` dependency, update constructor, and implement the 3 passthrough methods. Define `ErrNoConsensusHandler = errors.New("consensus handler not configured")`.
- `internal/server/simple/simple_posix_server.go`: Remove `metaRepl` (any) and `chunkRepl` from the struct and constructor. Replace the 3 Raft `handleMessage` cases with direct `cpo` delegation.
- `servers/node/wire_grpc_etcd.go`: Remove `chunkRepl` initialization and imports. Pass `metaRepl` directly into `NewControlPlaneOrchestrator` to enforce strict compile-time interface satisfaction.

**New Files (To be created for verification):**

- `internal/server/simple/simple_posix_server_test.go`: Unit tests for Raft RPC delegation.
- `internal/orchestrators/control_plane_test.go`: Unit tests for CPO passthrough and nil-handler error states.

**STRICTLY OFF-LIMITS (Do not touch):**

- `internal/metadata_service/*`: (Storage-only topology decoupling is explicitly deferred. Do not modify the metadata service's requirement for a replicator).
- `internal/metadata_replicator/durable_raft/*`: (Core consensus logic remains unchanged).

---

# Testing

The ultimate proof of this refactor is maintaining 100% backward compatibility with the hyper-converged baseline. We will not create new unit test files (e.g., no `simple_posix_server_test.go`). Instead, we rely entirely on the existing integration and smoke test harnesses.

**Verification Gates:**

1. **Compile Gate:** `env GOCACHE=/tmp/go-build go test ./...` must pass.
2. **POSIX Workflow Integration:** The `open_smoke` test suite must pass without any modifications to the client or test harness.
3. **Consensus & Routing Integration:** The `durability_smoke` test suite must pass, proving that the `SimpleServer` successfully delegates `RequestVote`, `AppendEntries`, and `InstallSnapshot` RPCs through the `ControlPlaneOrchestrator` to the underlying Raft replicator.

---

# Invariability Directives

*AI Directive: The following constraints resolve ambiguities found during static analysis. You must adhere to these strictly to guarantee zero-variability execution.*

1. **True Compile-Time Strictness (No Runtime Panics):**
Do NOT use a runtime type assertion check (e.g., `consensusHandler, ok := metaRepl.(...); if !ok { panic() }`). In `wire_grpc_etcd.go`, `metaRepl` is already instantiated as a concrete type. You must pass `metaRepl` *directly* into `NewControlPlaneOrchestrator`. Let the native Go compiler enforce interface satisfaction.
2. **Context Preservation:**
Network boundaries must not drop context. Ensure that `ctx context.Context` is the explicitly defined first parameter for all three methods inside the `ConsensusMessageHandler` interface, and pass it through accordingly in the CPO implementation.
3. **Exact Import Resolution:**
Do not leave `// Adjust imports` placeholders. You must scan the local workspace (specifically `types.go` and `communication` packages) to resolve and inject the exact, correct import paths for `RequestVoteArgs`, `AppendEntriesRequest`, and `InstallSnapshotRequest`.
4. **Topology Scope Deferral:**
This refactor strictly isolates the `SimpleServer` (the Gateway). Full "Storage-Only" topology decoupling (which would require modifying the `metadata_service`'s internal requirement for a replicator) is explicitly **Deferred to Phase 2**. Do not modify any files inside `internal/metadata_service/*` to achieve this.
5. **Nil-Handler Fallback:**
If the `consensusHandler` inside the `ControlPlaneOrchestrator` is nil, the passthrough methods must return a standard `errors.New("consensus handler not configured")`. The existing gRPC error mapping in `respond()` will surface this naturally; do not build custom error types.