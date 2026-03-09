# Smart Client Refactor

# Study Context

## 1. The Core Architectural Shift

Sandstore is transitioning from a static, tightly-coupled client model to a **Topology-Aware, Self-Healing Smart Client**. The current architecture bleeds internal server state (specifically Raft consensus errors and connection bootstrapping) directly to the application layer. The new architecture must encapsulate cluster discovery, leader routing, and transient failure recovery entirely within `sandlib`, presenting a seamless, POSIX-like interface to the end-user while supporting targeted initializations (e.g., hyper-converged vs. future GFS topologies).

## 2. Codebase Realities & The "Why"

Based on our repository extraction, the study phase must address three critical technical debts:

- **The Leak:** `sandlib` currently forces the user to manually construct transport stacks (`GRPCCommunicator`) and logging services (`LocalDiscLogService`).
- **The Bleed:** `SimpleServer` squashes structured Raft leader exceptions (`ErrNotLeader`) into a generic string-based `CodeInternal` envelope. This forces brittle string-parsing on the client side (`"leader is X at Y"`).
- **The Blindspot:** The server maintains highly accurate internal state (etcd liveness via `ClusterService` and leader state via `DurableRaftReplicator`), but lacks a dedicated RPC to expose this topology to the client.

## 3. Systems Engineering Concepts to Study

To execute this SDDD flawlessly, your study session should focus deeply on the following distributed systems paradigms:

### A. Client-Side Bootstrapping & Topology Discovery

- **The Seed Node Heuristic:** How distributed systems (like Cassandra, Kafka, or CockroachDB) use a subset of initial addresses to discover the full cluster.
- **Control Plane vs. Data Plane in the Client:** Designing a background mechanism (or lazy-evaluator) within the client that maintains a thread-safe routing table (mapping Node IDs to active gRPC connections) without blocking primary data I/O.

### B. Consensus Protocols from the Client's Perspective

- **Raft Leader Elections & Transient States:** Understanding the exact network lifecycle when a Raft leader steps down. What happens to in-flight TCP connections? How do we differentiate between a transient network partition, a node crash, and a graceful leadership transfer?
- **The Thundering Herd Problem:** When a leader dies, all concurrent client requests fail simultaneously. How do we implement jittered backoffs and prevent all goroutines from independently hammering the cluster for a topology refresh at the exact same millisecond?

### C. Safe Retries & Idempotency

- **The Danger of Blind Retries:** Why catching an error and simply retrying a `WriteRequest` or `CreateRequest` is highly dangerous in a distributed context.
- **Idempotency Keys / Sequence Numbers:** Exploring whether the client needs to inject idempotency tokens into RPCs, or if Sandstore's underlying storage engine can safely handle duplicated 2PC intents during split-brain scenarios.

### D. gRPC Advanced Mechanics

- **gRPC Interceptors (Middleware):** How to use UnaryClientInterceptors in Go to seamlessly catch specific gRPC error codes (e.g., a newly introduced `CodeNotLeader`), pause the request, trigger a topology refresh, and re-fire the RPC without the calling function ever knowing.
- **gRPC Connection State Machine:** Understanding `grpc.ClientConn` states (`Idle`, `Connecting`, `Ready`, `TransientFailure`) to effectively manage the connection pool to the various Sandstore nodes.

## 4. Target Personas Guiding the Design

- **The App Developer:** Expects zero friction; `sandlib` handles all discovery and retries transparently.
- **The Learner:** Expects clean boundaries; the client must be architected so internal retries can eventually be emitted as telemetry/metrics rather than swallowed silently.
- **The Core Contributor:** Expects strict interface definitions; the hyper-converged client must implement a generic `SandstoreClient` interface to allow for future topologies (like GFS) without rewriting the core library.

---

# Process

- [ ]  Goals → list out the goals of the project.
- [ ]  User stories → user centric design, 10 to 20 user stories for the project.
- [x]  Data models → define the data models and how the data models are going to be interacting with each other.
- [x]  New Wiring
- [x]  Drill into the specific components.
- [ ]  Codebase impact
- [ ]  How will the changes be tested?

---

# Goals

- **Encapsulate Distributed Complexity:** Prevent internal cluster mechanics (Raft elections, topology discovery, connection drops) from bleeding into the application layer.
- **Zero-Friction Initialization:** Allow developers to connect to the cluster using only a list of seed nodes, hiding transport and logging boilerplate.
- **Topology-Agnostic Routing:** Decouple the client's retry and routing logic from Raft-specific concepts (like "Leaders") so the client can natively support quorum-based (Dynamo) or centralized (GFS) topologies in the future.
- **Strict Idempotency Boundaries:** Ensure safe, silent retries for idempotent operations (Reads) while strictly failing fast on ambiguous mutations (Writes) to guarantee data integrity.

---

# User Stories

- **As an App Developer**, I want to initialize the Sandstore client by simply passing an array of seed addresses, so I don't have to manually configure gRPC communicators or internal loggers.
- **As an App Developer**, I want the client to automatically handle "Wrong Node" or "Not Leader" errors transparently, so my application doesn't crash during a routine cluster failover.
- **As an App Developer**, I want the client to immediately return an error if a network connection drops mid-`Write`, so I can safely handle the ambiguous state without the client blindly duplicating data.
- **As a Core Contributor**, I want the retry logic (Request Manager) strictly separated from the cluster map logic (Router), so I can write a new `DynamoRouter` in the future without having to rewrite the retry loops.
- **As an Automation Engineer**, I want the existing `open_smoke` and `durability_smoke` tests to continue passing without modification to their core assertions, proving the new client maintains backwards compatibility.

---

# Data Models

This section defines the strict Go structs and interfaces for the internal layers of the Smart Client, explicitly avoiding import cycles by keeping the `topology` package completely isolated from the `communication` package.

```go
// --- Package: sandlib/topology ---
package topology

import "context"

// 1. Error Normalization (No external dependencies)
type ErrorClass string

const (
	ClassExplicitRejection ErrorClass = "EXPLICIT_REJECTION" // Safe to retry (e.g., Not Leader, Wrong Shard)
	ClassAmbiguousFailure  ErrorClass = "AMBIGUOUS_FAILURE"  // Unsafe for mutations (e.g., Timeout)
	ClassTransportFailure  ErrorClass = "TRANSPORT_FAILURE"  // Connection dropped
	ClassSemanticError     ErrorClass = "SEMANTIC_ERROR"     // e.g., File Not Found
)

// 2. Gateway Ping Payloads 
// MsgTopologyRequest is JSON encoded into communication.MessageRequest.Payload
type MsgTopologyRequest struct {
	TopologyType string `json:"topology_type"`
	TopologyData []byte `json:"topology_data,omitempty"`
}

// MsgTopologyResponse is JSON encoded into communication.MessageResponse.Body
type MsgTopologyResponse struct {
	TopologyData []byte `json:"topology_data"`
}

// 3. Router Interface (Topology Agnostic)
type Router interface {
	GetRoute(isMutation bool) (address string, err error)
	Invalidate(failedAddr string)
	SetRouteHint(hint string)
	Refresh(ctx context.Context) error
}

// --- Package: sandlib (Client Internal) ---
package sandlib

import (
	"context"
	"sandstore/communication"
	"sandstore/topology"
)

// 4. Internal Error Model & Translator
type SystemError struct {
	Class       topology.ErrorClass
	WrappedErr  error
	RoutingHint string 
}

func (e *SystemError) Error() string { return e.WrappedErr.Error() }

type ErrorTranslator interface {
	Translate(resp *communication.Response, rawErr error) *SystemError
}

// 5. Request Manager
type RequestManager interface {
	ExecuteIdempotent(ctx context.Context, msgType string, payload any) (*communication.Response, error)
	ExecuteMutation(ctx context.Context, msgType string, payload any) (*communication.Response, error)
}
```

---

# New Wiring

This section explicitly maps the structural changes, public API context-wrapping, and the exact `ConvergedRouter` struct to eliminate implementation guesswork and maintain backwards compatibility with existing tests.

### 1. The Concrete Router Struct

To prevent lock-upgrade deadlocks and reader stalls during failovers, the `ConvergedRouter` uses two distinct locks: an `RWMutex` for fast concurrent route lookups, and a standard `Mutex` strictly to serialize network refreshes.

```go
// --- Package: sandlib/topology ---

import (
	"sync"
	"time"
	"sandstore/communication"
)

type ConvergedRouter struct {
	seeds []string
	comm  communication.Communicator
	
	mu         sync.RWMutex
	leaderAddr string
	allNodes   []string
	lastUpdate time.Time
	
	refreshMu  sync.Mutex // Prevents concurrent network calls (Thundering Herd mitigation)
}

func NewConvergedRouter(seeds []string, comm communication.Communicator) *ConvergedRouter {
	return &ConvergedRouter{
		seeds: seeds,
		comm:  comm,
	}
}
```

### 2. Client Initialization & Context Wrapping

The public API signature (`NewSandstoreClient`) is updated to take a slice of seeds. To maintain backwards compatibility for the App Developer, the core POSIX-like methods remain context-free but internally wrap operations with a strict timeout before calling the `RequestManager`.

```go
// --- Package: sandlib ---

import (
	"context"
	"fmt"
	"time"
	"sandstore/communication"
	"sandstore/topology"
)

func NewSandstoreClient(seeds []string, comm communication.Communicator) (*SandstoreClient, error) {
	router := topology.NewConvergedRouter(seeds, comm)
	
	// Force initial bootstrap with strict timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := router.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("initial topology bootstrap failed: %w", err)
	}
	
	translator := &DefaultErrorTranslator{}
	reqMgr := NewStandardRequestManager(router, comm, translator)
	
	return &SandstoreClient{
		reqManager: reqMgr,
		Comm:       comm, // Kept for backwards compatibility if needed
		OpenFiles:  make(map[uint64]*SandstoreFD),
	}, nil
}

// Example Public API Context Injection
func (c *SandstoreClient) Write(fd int, data []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	// ... existing buffer logic and payload construction ...
	
	_, err := c.reqManager.ExecuteMutation(ctx, "write", payload)
	if err != nil {
		return 0, err
	}
	
	return len(data), nil
}
```

### 3. Server-Side Integration (Blast Radius)

To answer the `MsgTopologyRequest` without widening the `ControlPlaneOrchestrator` interface or breaking its constructor, we introduce a targeted local interface inside the server package.

- **File:** `internal/server/simple_posix_server.go`
- **Changes:**
    1. Define a local interface:Go
        
        `type TopologyProvider interface {
            GetLeaderAddress() string
        }`
        
    2. Inject `TopologyProvider` into the `SimpleServer` struct during its construction in `wire_grpc_etcd.go` (passing the existing `DurableRaftReplicator`).
    3. In `handleMessage()`, explicitly catch `MsgTopologyRequest` (value: `"topology_request"`) and respond with the `TopologyProvider.GetLeaderAddress()` string packed into a `MsgTopologyResponse` JSON envelope.

---

# Component Flows

This section defines the exact execution logic, concurrency controls, and translation mechanics for the Smart Client. It eliminates implementation variability by explicitly defining the internal state modifications, network backoffs, and error mappings.

### 1. Router State & Concurrency (The Gateway Ping)

This defines how the `ConvergedRouter` safely manages state and prevents lock-upgrade deadlocks and thundering herds during a cluster failover.

```go
// --- Package: sandlib/topology ---

func (r *ConvergedRouter) GetRoute(isMutation bool) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.leaderAddr == "" {
		return "", fmt.Errorf("no known route available")
	}
	// For MVP Converged topology, all traffic routes to the Leader.
	return r.leaderAddr, nil
}

func (r *ConvergedRouter) Invalidate(failedAddr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leaderAddr == failedAddr {
		r.leaderAddr = "" // Force a refresh on the next GetRoute
	}
}

func (r *ConvergedRouter) SetRouteHint(hint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaderAddr = hint
	r.lastUpdate = time.Now()
}

func (r *ConvergedRouter) fetchTopologyFromSeeds(ctx context.Context) (string, []string, error) {
	// Iterate seeds in order. Return on first successful Gateway Ping.
	for _, seed := range r.seeds {
		req := communication.Message{
			From:    "sandlib",
			Type:    "topology_request",
			Payload: []byte(`{"topology_type":"converged"}`),
		}
		
		resp, err := r.comm.Send(ctx, seed, req)
		if err == nil && string(resp.Code) == communication.CodeOK {
			var topoMsg MsgTopologyResponse
			if err := json.Unmarshal(resp.Body, &topoMsg); err == nil {
				// For converged MVP, the raw body IS the leader address string
				return string(topoMsg.TopologyData), r.seeds, nil 
			}
		}
	}
	return "", nil, fmt.Errorf("all seeds failed topology ping")
}

func (r *ConvergedRouter) Refresh(ctx context.Context) error {
	// 1. Serialize network refreshes to prevent Thundering Herd
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	// 2. Double-check if another goroutine just refreshed the cache
	r.mu.RLock()
	isFresh := time.Since(r.lastUpdate) < 1*time.Second
	r.mu.RUnlock()
	if isFresh {
		return nil 
	}

	// 3. Perform Network I/O WITHOUT holding the RWMutex
	newLeader, newNodes, err := r.fetchTopologyFromSeeds(ctx)
	if err != nil {
		return err
	}

	// 4. Safely update the map instantly
	r.mu.Lock()
	r.leaderAddr = newLeader
	r.allNodes = newNodes
	r.lastUpdate = time.Now()
	r.mu.Unlock()

	return nil
}
```

### 2. Error Translation & Backoff Helpers

This strictly defines how raw gRPC/Server errors map to our internal `ErrorClass`, and how the jittered backoff is calculated.

```go
// --- Package: sandlib ---

func calculateJitter(attempt int) time.Duration {
	base := 100 * time.Millisecond
	maxWait := 1000 * time.Millisecond
	
	wait := base * time.Duration(1<<attempt) // Exponential backoff
	if wait > maxWait {
		wait = maxWait
	}
	
	// Add random jitter between 0 and 50ms
	jitter := time.Duration(rand.Intn(50)) * time.Millisecond
	return wait + jitter
}

type DefaultErrorTranslator struct{}

func (t *DefaultErrorTranslator) Translate(resp *communication.Response, rawErr error) *SystemError {
	// 1. Handle Hard Transport/Network Drops
	if rawErr != nil {
		if errors.Is(rawErr, context.DeadlineExceeded) || errors.Is(rawErr, context.Canceled) {
			return &SystemError{Class: topology.ClassAmbiguousFailure, WrappedErr: rawErr}
		}
		return &SystemError{Class: topology.ClassTransportFailure, WrappedErr: rawErr}
	}

	// 2. Handle Server-Side Codes
	code := string(resp.Code)
	body := string(resp.Body)

	switch code {
	case communication.CodeOK:
		return nil // Success
	case communication.CodeNotFound, communication.CodeAlreadyExists:
		return &SystemError{Class: topology.ClassSemanticError, WrappedErr: fmt.Errorf("semantic error: %s", body)}
	case "NOT_LEADER", "WRONG_NODE": 
		return &SystemError{
			Class:       topology.ClassExplicitRejection,
			WrappedErr:  fmt.Errorf("explicit rejection: %s", body),
			RoutingHint: body, // Server passes the correct route in the body
		}
	default:
		// Default to Ambiguous to fail safely on unknown errors
		return &SystemError{Class: topology.ClassAmbiguousFailure, WrappedErr: fmt.Errorf("unknown server error (%s): %s", code, body)}
	}
}
```

### 3. The Request Manager Retry Loops

These are the critical execution paths. They enforce the 3-retry budget, construct the correct wire `Message`, and safely branch based on the operation's idempotency.

```go
// --- Package: sandlib ---

// ExecuteMutation strictly fails fast on Transport/Ambiguous failures.
func (rm *StandardRequestManager) ExecuteMutation(ctx context.Context, msgType string, payload any) (*communication.Response, error) {
	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		targetAddr, err := rm.router.GetRoute(true)
		if err != nil {
			return nil, err
		}

		reqMsg := communication.Message{
			From:    "sandlib",
			Type:    msgType,
			Payload: payload,
		}
		rawResp, rawErr := rm.comm.Send(ctx, targetAddr, reqMsg)
		
		sysErr := rm.translator.Translate(rawResp, rawErr)
		if sysErr == nil {
			return rawResp, nil 
		}

		switch sysErr.Class {
		case topology.ClassSemanticError:
			return nil, sysErr.WrappedErr

		case topology.ClassAmbiguousFailure, topology.ClassTransportFailure:
			// Unsafe to retry a mutation! Bubble up immediately.
			rm.router.Invalidate(targetAddr)
			return nil, sysErr.WrappedErr

		case topology.ClassExplicitRejection:
			// Safe to retry. The server cleanly rejected it before execution.
			if sysErr.RoutingHint != "" {
				rm.router.SetRouteHint(sysErr.RoutingHint)
			} else {
				_ = rm.router.Refresh(ctx)
			}
			
			lastErr = sysErr.WrappedErr
			time.Sleep(calculateJitter(attempt))
			continue
			
		default:
			return nil, sysErr.WrappedErr
		}
	}

	return nil, fmt.Errorf("mutation failed after %d attempts. last error: %w", maxRetries, lastErr)
}

// ExecuteIdempotent safely retries on Transport failures.
func (rm *StandardRequestManager) ExecuteIdempotent(ctx context.Context, msgType string, payload any) (*communication.Response, error) {
	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		targetAddr, err := rm.router.GetRoute(false)
		if err != nil {
			return nil, err
		}

		reqMsg := communication.Message{
			From:    "sandlib",
			Type:    msgType,
			Payload: payload,
		}
		rawResp, rawErr := rm.comm.Send(ctx, targetAddr, reqMsg)
		
		sysErr := rm.translator.Translate(rawResp, rawErr)
		if sysErr == nil {
			return rawResp, nil
		}

		switch sysErr.Class {
		case topology.ClassSemanticError:
			return nil, sysErr.WrappedErr

		case topology.ClassAmbiguousFailure:
			// Still unsafe (e.g. context timeout might just be a slow read), bubble up.
			return nil, sysErr.WrappedErr

		case topology.ClassExplicitRejection, topology.ClassTransportFailure:
			// DIFFERENT FROM MUTATION: Dropping a connection is safe to retry for Reads!
			rm.router.Invalidate(targetAddr)
			
			if sysErr.RoutingHint != "" {
				rm.router.SetRouteHint(sysErr.RoutingHint)
			} else {
				_ = rm.router.Refresh(ctx)
			}
			
			lastErr = sysErr.WrappedErr
			time.Sleep(calculateJitter(attempt))
			continue
			
		default:
			return nil, sysErr.WrappedErr
		}
	}

	return nil, fmt.Errorf("idempotent op failed after %d attempts. last error: %w", maxRetries, lastErr)
}
```

---

## 4. Implementation Mapping & Type Safety Patch

*This section resolves all micro-level structural ambiguities, exact file paths, and type safety constraints for the coding AI.*

### A. Exact File Inventory & Package Layout

Do not guess file locations. The new client components must be distributed exactly as follows:

1. **`clients/library/topology/models.go`**: Contains `ErrorClass`, explicit error constants, `MsgTopologyRequest`, `MsgTopologyResponse`, and the `Router` interface.
2. **`clients/library/topology/converged_router.go`**: Contains the `ConvergedRouter` struct, `NewConvergedRouter`, and its methods.
3. **`clients/library/errors.go`**: Contains `SystemError`, `ErrorTranslator` interface, and `DefaultErrorTranslator`.
4. **`clients/library/request_manager.go`**: Contains `RequestManager` interface, `StandardRequestManager` struct, constructor, and retry loop methods. Includes the `calculateJitter` helper.
5. **`clients/library/types.go`**: Update the existing `SandstoreClient` struct here.

### B. The Missing Client & Manager Structs

Update `types.go` and define the Request Manager explicitly.

**In `clients/library/types.go`:**

Go

`// Replace the existing SandstoreClient struct with this to fix initialization gaps:
type SandstoreClient struct {
	ServerAddr string // Kept for legacy method compatibility; populated with seed[0] or leader
	Comm       *grpccomm.GRPCCommunicator // Typed to exact existing struct
	
	reqManager RequestManager
	OpenFiles  map[uint64]*SandstoreFD
	TableMu    sync.RWMutex
}`

**In `clients/library/request_manager.go`:**

Go

`import (
    "context"
    "fmt"
    "time"
    "math/rand"
    
    "sandstore/communication" // Note: replace 'sandstore' with actual go.mod module name
    "sandstore/clients/library/topology"
)

type StandardRequestManager struct {
	router     topology.Router
	comm       communication.Communicator
	translator ErrorTranslator
}

func NewStandardRequestManager(r topology.Router, c communication.Communicator, t ErrorTranslator) *StandardRequestManager {
	return &StandardRequestManager{
		router:     r,
		comm:       c,
		translator: t,
	}
}`

### C. Type Safety & Nil Panic Fixes

Fix the type mismatches in the Router and Translator flows.

**Fix `fetchTopologyFromSeeds` (in `converged_router.go`):**

Go

`// 1. Properly encode the request struct into bytes
reqBody, _ := json.Marshal(MsgTopologyRequest{TopologyType: "converged"})
req := communication.Message{
    From:    "sandlib",
    Type:    "topology_request",
    Payload: reqBody,
}

resp, err := r.comm.Send(ctx, seed, req)
// 2. Fix type mismatch on CodeOK
if err == nil && resp != nil && resp.Code == string(communication.CodeOK) {
    // ... unmarshal logic ...
}`

**Fix `Translate` Nil Panic & Constants (in `errors.go`):**

Go

`func (t *DefaultErrorTranslator) Translate(resp *communication.Response, rawErr error) *SystemError {
	if rawErr != nil {
        // ... existing network drop logic ...
	}
    // 1. Fix Nil Panic
    if resp == nil {
        return &SystemError{Class: topology.ClassAmbiguousFailure, WrappedErr: fmt.Errorf("nil response with no error")}
    }

    // 2. Codes are strings
	code := resp.Code
    // ... switch statement ...
}`

### D. Server-Side Execution (Exact Hooks)

Do not guess how the server wires this. Follow these exact steps:

1. **Constants (`internal/server/messages.go`)**: Add `const MsgTopologyRequest = "topology_request"`. Add `const CodeNotLeader = "NOT_LEADER"` to `communication/communicator.go` (or wherever `CodeOK` lives).
2. **Registration (`internal/server/simple/simple_posix_server.go`)**: In `registerPayloads()`, add `s.payloadTypes[MsgTopologyRequest] = reflect.TypeOf(topology.MsgTopologyRequest{})`.
3. **The Replicator Patch (`replicator.go`)**: Add the missing method to `DurableRaftReplicator`:Go
    
    `func (r *DurableRaftReplicator) GetLeaderAddress() string {
        r.mu.RLock()
        defer r.mu.RUnlock()
        return r.findLeaderAddrLocked(r.leaderID)
    }`
    
4. **Server Constructor (`internal/server/simple/simple_posix_server.go`)**: Update `SimpleServer` struct to include `topology TopologyProvider`. Update `NewSimpleServer` to accept `TopologyProvider` as its last argument.
5. **Wiring (`wire_grpc_etcd.go`)**: When calling `NewSimpleServer(...)`, pass `metaRepl` (the `DurableRaftReplicator`) as the `TopologyProvider` argument.