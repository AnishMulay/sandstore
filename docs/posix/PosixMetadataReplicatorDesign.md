# Feature: POSIX Metadata Replicator Design

## Process

- [ ] **Goals** → Define the consensus responsibilities.
- [ ] **User stories** → Leader election and log consistency stories.
- [ ] **Data models** → The Log Entry and AppendEntries RPC structures.
- [ ] **Nail an MVP** → Interface definition + Simple "Push" implementation.
- [ ] **Draw the prototype** → The "Propose -> Commit -> Apply" loop.
- [ ] **Future** → Log Compaction (Snapshots), Read-Index optimization.
- [ ] **Drill into specific components** → The StateMachine callback interface.
- [ ] **Pick a stack** → Go + Hashicorp Raft (or custom Raft).
- [ ] **Actual software development.**

## Goals

### Vision and Purpose

The PosixMetadataReplicator abstracts the distributed consensus algorithm.

It guarantees **Total Order**: If Node A applies Op1 then Op2, Node B must also apply Op1 then Op2.

### Functional Goals

-   **Replication**: Ensure that an operation submitted to the Leader is copied to a quorum of followers before being executed.
-   **Leader Election**: If the leader fails, the replicator must coordinate to pick a new one.
-   **Recovery**: When a node restarts, the replicator must replay the log to restore the MetadataService state.

### Non-Functional Goals

-   **Pluggability**: The system should be able to run with a `NoOpReplicator` (for single-node testing) or a `RaftReplicator` (for production) without changing the MetadataService.
-   **Latency Hiding**: It should support pipelining log entries to minimize the wait time for consensus.

## User Stories (Consensus Focus)

> **The Proposal**: As the MetadataService, I want to `Replicate(Op)` and get a success/fail response only after the cluster has safely committed the operation, so I don't acknowledge a write that might get lost.

> **The Callback**: As the Replicator, when a log entry is committed, I want to call `StateMachine.Apply(Op)` so the MetadataService can update its memory maps.

> **The Catch-Up**: As a Follower Node, if I disconnect and reconnect, I want to automatically request missing log entries from the Leader so my state becomes consistent.

> **Leader Only**: As the Replicator, if I am a Follower and receive a `Replicate()` request, I want to reject it and point to the current Leader, enforcing that all writes go through one point.

## Data Models

The Replicator deals with **Logs**, not files.

### 1. Log Entry

The fundamental unit of storage.

```go
type LogEntry struct {
    Index   uint64 // Monotonically increasing ID
    Term    uint64 // Raft Term (Epoch)
    Command []byte // Serialized Operation from MetadataService
}
```

### 2. Replication RPCs (Internal)

These are the messages exchanged between Replicators (not Clients).

-   **RequestVote**: "I want to be leader."
-   **AppendEntries**: "Here are new logs for you to store."
-   **InstallSnapshot**: "You are too far behind, here is the full state."

## MVP

The MVP defines the rigid **Interface** and provides the Raft implementation structure (even if the internal logic is initially simplified).

-   **Interface**: `Replicate(ctx, data)`
-   **Raft Implementation**:
    -   Message Loop (Handle Vote/Append).
    -   Election Timer.
    -   Log Storage (In-memory slice for MVP).
    -   Commit Index tracking.

## Prototyping (The Consensus Loop)

This is the critical flow that connects the Service to the Replicator.

### The "Propose-Commit-Apply" Cycle:

1.  **Propose**:
    -   `MetadataService` calls `Replicator.Replicate(cmd)`.
    -   Replicator (Leader) appends `cmd` to its local Log.
    -   Replicator broadcasts `AppendEntries(cmd)` to followers.

2.  **Commit**:
    -   Followers respond: "Ack, I saved it."
    -   Leader waits for **Quorum** (Majority).
    -   Leader marks entry as **Committed**.

3.  **Apply**:
    -   Leader executes `StateMachine.Apply(cmd)` locally.
    -   Leader returns "Success" to `MetadataService`.
    -   Leader tells Followers "Index X is committed" (in next heartbeat).
    -   Followers execute `StateMachine.Apply(cmd)` locally.

## Project Future

### Future Extensions

-   **Read-Index / Leases**: Allow the Leader to serve Reads without a full consensus round-trip, provided it knows it still has the lease.
-   **Snapshots**: When the log grows too large (e.g., 10k entries), the Replicator will ask the `MetadataService` to dump its state to a file and truncate the old logs.
-   **Joint Consensus**: Support adding/removing servers dynamically without stopping the cluster.

## Specific Components

### 1. Interface Definition

`sandstore/internal/posix_metadata_replicator/replicator.go`

```go
package posixreplicator

import "context"

// PosixMetadataReplicator defines the consensus engine.
type PosixMetadataReplicator interface {
    // Start begins the background election/replication routines.
    Start(ctx context.Context) error
    
    // Replicate proposes a command to the cluster.
    // Returns nil if committed and applied.
    // Returns ErrNotLeader if this node is a follower.
    Replicate(ctx context.Context, command []byte) error
    
    // Stop performs a graceful shutdown.
    Stop() error
}

// StateMachine is the interface the Replicator uses to update the "Truth".
// The MetadataService must implement this.
type StateMachine interface {
    // Apply is called when a log entry is committed.
    // It must be deterministic.
    Apply(command []byte)
    
    // Snapshot/Restore methods will go here in the future.
}
```

### 2. Implementation (Raft Wrapper)

`sandstore/internal/posix_metadata_replicator/raft/raft_replicator.go`

You will need a struct that ties your `Communicator` to the Raft logic.

```go
type RaftReplicator struct {
    // Config
    nodeID    string
    peers     []string
    
    // Dependencies
    transport communication.Communicator // To send RPCs
    fsm       StateMachine               // The MetadataService
    
    // Internal State
    currentTerm uint64
    state       RaftState // Follower, Candidate, Leader
    log         []LogEntry
    commitIndex uint64
    lastApplied uint64
    
    // Channels for the event loop
    applyCh     chan LogEntry
}

func (r *RaftReplicator) Replicate(ctx context.Context, cmd []byte) error {
    if r.state != Leader {
        return ErrNotLeader
    }
    
    // 1. Append to local log
    // 2. Broadcast to peers
    // 3. Wait for consensus (using a channel or condition variable)
    // 4. Return result
}

// The runLoop handles incoming RPCs from the Communicator
func (r *RaftReplicator) runLoop() {
    for {
        select {
        case msg := <-r.transport.Recv():
            if msg.Type == MsgRequestVote {
                r.handleRequestVote(msg)
            } else if msg.Type == MsgAppendEntries {
                r.handleAppendEntries(msg)
            }
        case <-electionTimer.C:
            r.startElection()
        }
    }
}
```

## Stack

-   **Language**: Go.
-   **Library Choice**:
    -   **Option A (Learning)**: Implement the basic Raft loop yourself (Election + Log Replication). This matches your goal of a "Learning First" DFS.
    -   **Option B (Production)**: Use `hashicorp/raft`.

**Recommendation**: Since this is `sandstore` (a learning project), implementing a simplified Raft (Option A) is the correct path.
