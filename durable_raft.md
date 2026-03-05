# Durable Raft

# Context Block: Sandstore Persistent Raft & Log Durability

## 1. The Architectural Scope and Objective

This phase transitions Sandstore's consensus engine from a volatile, in-memory prototype (`RaftMetadataReplicator`) to a production-grade, durable implementation. The objective is to introduce a Write-Ahead Log (WAL), stable storage for Raft voting state, and log compaction via Snapshotting.

**Crucial Boundary:** This change is strictly isolated to the *Consensus Plane*. It must integrate seamlessly with the existing `pmr.MetadataReplicator` interface (`Start`, `Replicate`, `Stop`). The State Machine (recently upgraded to `BoltMetadataService`) remains entirely black-boxed from Raft's internal durability mechanics. Raft's only job is to durably commit logs and pass them to the state machine via the `pmr.ApplyFunc`.

## 2. The Core Problem: Volatile State and Amnesia

Currently, the Raft node's state is strictly in-memory:

- `log []LogEntry`: Erased on crash.
- `currentTerm` and `votedFor`: Erased on crash.

In the Raft protocol, a node *must* remember its `currentTerm` and who it `votedFor` across restarts to guarantee the safety property (preventing two leaders from being elected in the same term). Furthermore, appending to the log must be durably synced to disk before acknowledging success to the leader or client. Without this, the system suffers from "amnesia," violating the foundational guarantees required for publishable benchmarking.

## 3. Storage Engine Abstractions (The New Seams)

To fix this without hardcoding file I/O directly into the Raft event loop, we must introduce explicit storage interfaces that the `RaftMetadataReplicator` will consume.

- **`LogStore` Interface:** A robust abstraction for an append-only Write-Ahead Log. It must support `StoreLogs(entries []LogEntry)`, `GetLog(index int64)`, and `DeleteRange(min, max int64)` for future compaction.
- **`StableStore` Interface:** A simple Key-Value abstraction to durably persist the `currentTerm` and `votedFor` strings.

*Note: While Phase 1 of this refactor requires defining these interfaces and wiring Raft to use them, the actual implementation could be backed by a dedicated library (like `etcd/bbolt` or a custom append-only file mechanism) designed for low-latency `fsync` operations.*

## 4. Snapshotting and Log Compaction

Because the filesystem will run indefinitely, the Raft log cannot grow without bound. We must introduce snapshotting to truncate the WAL.

- **The State Machine Connection:** Raft itself does not know what the filesystem looks like. When the log grows too large, Raft must ask the `BoltMetadataService` (via a new interface/callback) for a serialized snapshot of its current B+Tree state.
- **The `InstallSnapshot` RPC:** We must extend the `communication.Communicator` and `ps.MsgRaft...` payloads to support `InstallSnapshotArgs` and `InstallSnapshotReply`. This allows a leader to bring a severely lagging follower up to speed by sending the entire filesystem namespace state, bypassing thousands of historical log entries.

## 5. Study Session Goals

By the end of the study session utilizing this context, the engineer must deeply understand:

1. **The Fsync Penalty:** How synchronous disk writes (`fsync`) for every `AppendEntries` RPC will impact cluster throughput, and why batching is often necessary in production Raft implementations.
2. **Safety vs. Liveness on Restart:** Exactly why a node *must* read its persistent state *before* it begins participating in elections or responding to RPCs.
3. **The Compaction Handshake:** The exact sequence of events required to safely truncate the `LogStore` after the state machine has successfully snapshotted its B+Tree to disk.
4. **Handling `InstallSnapshot`:** How an incoming snapshot from a leader overrides the local follower's `BoltMetadataService` state, and how to safely swap the underlying database file.

---

## Process

- [x]  Goals → list out the goals of the project.
- [x]  User stories → user centric design, 10 to 20 user stories for the project.
- [x]  Data models → define the data models and how the data models are going to be interacting with each other.
- [x]  What does the future of the project look like ?
- [x]  Drill into the specific components.
- [ ]  How will the changes be tested?
- [ ]  Pick a stack → programming language, frameworks etc.
- [ ]  Actual software development.

---

# Goals

**Primary Objectives:**

- **Production-Grade Durability:** Transition Sandstore's consensus engine from a volatile, in-memory prototype to a fully durable, disk-backed implementation.
- **Irrefutable Benchmarking:** Eliminate state "amnesia" on node crash. Guarantee strict Raft safety properties so that upcoming benchmarking tests and the resulting white paper are academically and technically sound.

**Secondary & Future-Enabling Objectives:**

- **Log Compaction:** Prevent unbounded Write-Ahead Log (WAL) growth by implementing Snapshotting to truncate historical logs.
- **State Reconciliation (`InstallSnapshot`):** Implement the `InstallSnapshot` RPC to allow leaders to bring lagging or newly provisioned followers up to speed using full filesystem namespace states.
- **Dynamic Cluster Foundation:** By completing snapshotting and state transfer mechanisms, lay the necessary groundwork for future support of dynamic cluster membership (adding/removing nodes on the fly).

---

# User Stories

### A. Stable State & Amnesia Prevention (`StableStore`)

1. **[Happy Path - State Change]** **As a** Raft node, **when I** update my `currentTerm` or grant a vote (`votedFor`) during an election, **I want** to durably write and `fsync` this change to the `StableStore` before taking any further action, **so that** I guarantee I never vote twice in the same term, even if I crash a millisecond later.
2. **[Happy Path - Bootstrapping]** **As a** rebooting Raft node, **when I** initialize after a crash, **I want** to read my persisted `currentTerm` and `votedFor` from the `StableStore` before starting my event loop, **so that** I safely resume cluster participation without amnesia.
3. **[Sad Path - I/O Failure]** **As a** Raft node, **when I** attempt to update my `StableStore` but encounter a disk/I/O error, **I want** to panic or halt immediately, **so that** I do not violate Raft's strict safety guarantees by continuing to operate on volatile or corrupted state.

### B. Log Durability & Replication (`LogStore`)

1. **[Happy Path - Leader Batching]** **As a** Leader, **when I** receive a high volume of client requests, **I want** to batch the new log entries and amortize the `fsync` operation to my `LogStore`, **so that** I maximize throughput without sacrificing disk durability.
2. **[Sad Path - Leader Write Failure]** **As a** Leader, **when I** fail to durably append a client request to my local `LogStore` (e.g., out of disk space), **I want** to immediately return a failure to the client and *not* advance my local `matchIndex`, **so that** the system correctly recognizes the log as uncommitted.
3. **[Happy Path - Follower Replication]** **As a** Follower, **when I** receive an `AppendEntries` RPC, **I want** to durably write the new log entries to my `LogStore` before replying with a success acknowledgment, **so that** the Leader can safely and mathematically count my replica toward the commit quorum.
4. **[Sad Path - Follower Write Failure]** **As a** Follower, **when I** fail to durably persist incoming `AppendEntries` to disk, **I want** to reply with a failure to the Leader, **so that** the Leader knows my replica is out of sync and will retry replication later.

### C. Log Compaction & Snapshots

1. **[Happy Path - Compaction Trigger]** **As a** Raft node, **when I** detect that my Write-Ahead Log has exceeded a configured size threshold, **I want** to signal the State Machine to serialize and save its state to disk, **so that** I can safely delete historical logs and prevent unbounded disk usage.
2. **[Sad Path - Failed Snapshot]** **As a** Raft node, **when I** trigger compaction but the State Machine fails to generate or save the snapshot, **I want** to safely abort the log truncation process, **so that** I do not delete historical logs that haven't been durably checkpointed.
3. **[Happy Path - Leader Detects Lag]** **As a** Leader, **when I** prepare to send `AppendEntries` but realize a follower's `nextIndex` points to a log I have already compacted, **I want** to seamlessly transition to sending an `InstallSnapshot` RPC, **so that** the follower can bridge the gap in their history.
4. **[Happy Path - New/Lagging Node Joins]** **As a** lagging or newly provisioned Follower, **when I** receive an `InstallSnapshot` RPC, **I want** to safely overwrite my local State Machine state and truncate my local `LogStore`, **so that** I can seamlessly jump forward in time to match the cluster's current state.
5. **[Sad Path - Corrupted Snapshot Reception]** **As a** Follower, **when I** receive an `InstallSnapshot` RPC but encounter an error while saving the snapshot to disk, **I want** to reject the RPC and retain my previous state, **so that** I do not corrupt my underlying database file.

---

# Data Models

To eliminate state amnesia and guarantee strict Raft safety properties, we must decouple the consensus engine's state from volatile memory. The following interfaces define the strict storage contracts the `RaftMetadataReplicator` will rely on. By expressing these as interfaces rather than concrete file I/O operations, we maintain a clean architectural boundary: Raft dictates *when* data must be durable to satisfy the protocol, while the underlying storage engine (injected at startup) dictates *how* it is written to disk.

We divide the persistent state into three distinct domains based on their access patterns. The `StableStore` manages low-volume, synchronous writes for critical election state (`currentTerm` and `votedFor`). The `LogStore` serves as a Write-Ahead Log (WAL) optimized for high-throughput, append-only sequential writes. Finally, the `SnapshotStore` provides an opaque boundary between Raft and the State Machine, allowing Raft to manage log compaction (truncation) without needing to understand the underlying B+Tree data structures.

```jsx
// 1. StableStore: Persists the voting and term state
// Must guarantee synchronous durability (fsync) on every write.
type StableStore interface {
    SetState(currentTerm uint64, votedFor string) error
    GetState() (currentTerm uint64, votedFor string, err error)
}

// 2. LogStore (WAL): Append-only log persistence
type LogStore interface {
    // Stores a batch of logs. Must fsync before returning.
    StoreLogs(entries []LogEntry) error 
    
    // Retrieves a specific log by its Raft index
    GetLog(index uint64) (LogEntry, error)
    
    // Returns the metadata of the last log in the WAL
    LastIndexAndTerm() (index uint64, term uint64, err error)
    
    // Deletes logs up to the snapshot index (Log Compaction)
    DeleteRange(min uint64, max uint64) error 
}

// 3. Snapshot Data Models
// Raft only cares about the metadata. The data itself is a black box []byte.
type SnapshotMeta struct {
    LastIncludedIndex uint64
    LastIncludedTerm  uint64
}

// SnapshotStore handles saving the opaque B+Tree state to disk
type SnapshotStore interface {
    SaveSnapshot(meta SnapshotMeta, stateMachineData []byte) error
    LoadSnapshot() (meta SnapshotMeta, stateMachineData []byte, err error)
}
```

To amortize the "fsync penalty" and manage disk usage safely, Raft requires specific I/O tuning parameters. Crucially, these parameters must **not** be managed dynamically by the cluster state (which creates a circular dependency) but rather injected as static configuration at process startup (e.g., via Environment Variables in Docker/Kubernetes).

```jsx
// RaftConfig defines the static I/O and tuning parameters for the node.
type RaftConfig struct {
    // --- Batching & I/O Amortization ---
    // MaxBatchSize dictates the maximum number of logs to buffer before forcing an fsync.
    MaxBatchSize int 
    
    // MaxBatchWaitTime dictates the maximum time to wait for the buffer to fill 
    // before flushing and fsyncing to the LogStore. 
    MaxBatchWaitTime time.Duration 

    // --- Compaction & Snapshotting ---
    // SnapshotThresholdLogs defines how many log entries the LogStore can hold 
    // before triggering the State Machine to generate a snapshot.
    SnapshotThresholdLogs uint64 
}
```

---

# Future

The transition to a Durable Raft implementation is the foundational prerequisite for Sandstore's next major milestone: irrefutable performance benchmarking and the publication of a comprehensive white paper. To be taken seriously by the distributed systems community and drive adoption, Sandstore must prove its performance under real-world constraints, which strictly requires paying the "fsync penalty" for synchronous disk writes. If the consensus engine relies on volatile memory, any performance claims will be instantly dismissed as fundamentally flawed. By building a rigorously correct, durable Raft engine with log compaction, we ensure our future benchmarks are academically and technically sound, paving the way for a robust stress-testing suite and widespread community trust.

---

# Sepcific Component Flows

To ensure system stability and prevent scope creep, the implementation of Durable Raft MUST adhere to the following strict constraints:

1. **Interface Immutability:** Do NOT modify the existing `MetadataReplicator` interface
2. **State Machine Isolation:** Do NOT modify the underlying state machine (`BoltMetadataService`), cluster configuration, or network transports. The state machine remains a black box.
3. **New Implementation Only:** The ONLY structural change is creating a new struct (e.g., `DurableRaftReplicator`) that implements `MetadataReplicator`.
4. **Initialization Changes:** The only external modification allowed is at the dependency injection level (in the wire_grpc_etcd.go file where the simple server is created), where the new `DurableRaftReplicator` is instantiated with `RaftConfig`, `LogStore`, `StableStore`, and `SnapshotStore`.
5. **State Machine Snapshot Contract (The Exception):** While the core logic of the state machine (`BoltMetadataService`) remains entirely black-boxed (Invariant 2), we MUST extend its API to support Raft's log compaction. We will define a new interface extension (e.g., `SnapshotableStateMachine`) that requires `SerializeState() ([]byte, error)` and `RestoreState([]byte) error`. `BoltMetadataService` will be the only component updated to implement these new methods.

### Flow 1: Leader Receives Client Command

```jsx
TRIGGER: Client/State Machine calls Replicate(Command) on the Leader.

1. RECEIVE & BUFFER:
   - Append `Command` to an in-memory `pendingBatch` array.
   - Do NOT immediately execute disk I/O.

2. BATCHING TRIGGER:
   - A background goroutine (the event loop) monitors the buffer.
   - Proceed to Step 3 ONLY IF:
     a) len(pendingBatch) >= RaftConfig.MaxBatchSize, OR
     b) RaftConfig.MaxBatchWaitTime has elapsed since the first item was buffered.

3. DURABLE PERSISTENCE (FSYNC):
   - Call `LogStore.StoreLogs(pendingBatch)`.
   - ERROR HANDLING: If `StoreLogs` returns an error (e.g., disk full):
     - Immediately return failure/error to all clients associated with this batch.
     - Empty the `pendingBatch`.
     - Do NOT advance the leader's log index. Abort this flow.

4. BROADCAST:
   - Construct `AppendEntries` RPCs containing the newly persisted logs.
   - Send RPCs to all Followers concurrently.

5. QUORUM & COMMIT:
   - Wait for `Success == true` responses from a majority of nodes (including self).
   - Once quorum is reached, update Leader's `commitIndex`.

6. APPLY:
   - For each newly committed log, invoke the state machine via `pmr.ApplyFunc`.
   - Reply `Success` to the waiting clients.
```

### Flow 2: Follower Receives AppendEntries RPC

```jsx
TRIGGER: Follower receives AppendEntries RPC from Leader.

1. TERM CHECK:
   - IF RPC.Term < Follower.currentTerm: Return {Success: false, Term: Follower.currentTerm}.
   - Or if the term is the same but the followers log is larger 
   - This is more or less already implemented. You can look at the currently implemented in-memory raft version. 

2. LOG MATCHING PROPERTY (SAFETY):
   - IF Follower's log does not contain an entry at `RPC.PrevLogIndex`:
     - Return {Success: false}.
   - IF Follower's log has an entry at `RPC.PrevLogIndex` but its term does NOT match `RPC.PrevLogTerm`:
     - Return {Success: false}.

3. CONFLICT RESOLUTION:
   - Iterate through new entries in the RPC.
   - IF an existing log entry conflicts with a new one (same index, different term):
     - Delete the existing entry and ALL subsequent entries in the Follower's log.

4. DURABLE PERSISTENCE (FSYNC):
   - Append any new entries not already in the log to a local `newLogs` array.
   - Call `LogStore.StoreLogs(newLogs)`.
   - ERROR HANDLING: IF `StoreLogs` fails, return {Success: false} immediately.

5. COMMIT & APPLY:
   - IF `RPC.LeaderCommit > Follower.commitIndex`:
     - Set `Follower.commitIndex = min(RPC.LeaderCommit, index of last new entry)`.
     - Apply all unapplied committed logs to the State Machine via `pmr.ApplyFunc`.

6. ACKNOWLEDGE:
   - Return {Success: true, Term: Follower.currentTerm}.
```

### Flow 3: Crash Recovery and Initialization

```jsx
TRIGGER: Node process restarts and initializes the DurableRaftReplicator constructor.

1. LOAD STABLE STATE:
   - Call `StableStore.GetState()`.
   - Populate in-memory `currentTerm` and `votedFor`.
   - IF I/O error occurs: Panic/Halt. Do not start the node.

2. LOAD LOG STATE:
   - Call `LogStore.LastIndexAndTerm()`.
   - Populate in-memory log metadata so the node knows its latest state.
   - IF I/O error occurs: Panic/Halt.

3. LOAD SNAPSHOT STATE (IF ANY):
   - Call `SnapshotStore.LoadSnapshot()`.
   - IF a snapshot exists, initialize `lastIncludedIndex` and `lastIncludedTerm`.

4. INITIALIZE VOLATILE STATE:
   - Set `commitIndex = 0` (or `lastIncludedIndex` if starting from snapshot).
   - Set `lastApplied = 0` (or `lastIncludedIndex` if starting from snapshot).
   
5. START EVENT LOOP:
   - Node is now safe to begin listening for RPCs, starting election timers, etc.
```

### FLow 4: Log Compaction

```jsx
TRIGGER: Background routine detects LogStore count > RaftConfig.SnapshotThresholdLogs.

1. PREPARE SNAPSHOT METADATA:
   - Capture `currentIndex = Follower.lastApplied` and `currentTerm = term at lastApplied`.
   - (Leader and Followers do this independently based on their own applied state).

2. REQUEST STATE MACHINE SNAPSHOT:
   - Ask the State Machine (via callback/interface) to serialize its state to `[]byte`.
   
3. DURABLE SNAPSHOT PERSISTENCE:
   - Construct `SnapshotMeta{LastIncludedIndex: currentIndex, LastIncludedTerm: currentTerm}`.
   - Call `SnapshotStore.SaveSnapshot(SnapshotMeta, stateMachineBytes)`.
   - (This must write to a temp file and do an atomic rename internally).
   - IF failure occurs: Abort compaction flow. Do not delete logs.

4. LOG TRUNCATION:
   - ONLY AFTER `SaveSnapshot` succeeds:
   - Call `LogStore.DeleteRange(0, currentIndex)`.
   - Update in-memory log offsets to reflect the truncated WAL.
```

### Flow 5: InstallSnapshot RPC

```jsx
TRIGGER: Leader determines Follower's `nextIndex` is <= Leader's `lastIncludedIndex` (log was compacted).

[LEADER SIDE]
1. Leader constructs `InstallSnapshot` RPC containing:
   - `Term`, `LeaderId`.
   - `LastIncludedIndex`, `LastIncludedTerm`.
   - The raw bytes of the snapshot from `SnapshotStore`.
2. Leader sends RPC to lagging Follower.

[FOLLOWER SIDE - RECEIVING RPC]
1. TERM CHECK:
   - IF RPC.Term < Follower.currentTerm: Return {Term: Follower.currentTerm} (Reject).

2. EXISTING LOG CHECK (RAFT OPTIMIZATION):
   - IF Follower's log has an entry at `RPC.LastIncludedIndex` AND its term matches `RPC.LastIncludedTerm`:
     - Retain log entries following `RPC.LastIncludedIndex`.
     - Fast-forward `commitIndex` to `RPC.LastIncludedIndex`.
     - Reply Success. (Skip state overwrite).

3. DURABLE SNAPSHOT OVERWRITE (The Catch-Up):
   - Call `SnapshotStore.SaveSnapshot(RPC.LastIncludedIndex, RPC.LastIncludedTerm, RPC.Data)`.
   - Discard the entire existing `LogStore` (call `DeleteRange(0, latest)`).
   
4. STATE MACHINE RECONCILIATION:
   - Pass the new snapshot bytes to the State Machine to reset its B+Tree.
   - Set `commitIndex = RPC.LastIncludedIndex`.
   - Set `lastApplied = RPC.LastIncludedIndex`.
```

---

# Testing

To validate the "Irrefutable Benchmarking" goal and ensure zero data loss (amnesia prevention), we will implement a new automated chaos-testing harness: `durability_smoke`.

Rather than testing simple POSIX compliance like `open_smoke`, this suite will verify that Sandstore's metadata survives hard crashes, split-brain scenarios, and mid-fsync power losses.

### Framework Architecture

We will leverage the existing Docker and gRPC infrastructure to build this harness:

- **Environment:** We will duplicate `deploy/docker/docker-compose.yaml` to create `docker-compose-durability.yaml`. This environment will spin up the ETCD instance and the 3 Sandstore nodes, but will expose the Docker socket (or use shell scripts) so the test runner can arbitrarily `docker kill` specific containers.
- **Injection Point:** The new `DurableRaftReplicator` will be injected exclusively in `servers/node/wire_grpc_etcd.go` (around line 70), fully adhering to the Invariants defined in Section 4.
- **Test Runner:** A new binary will be created at `clients/durability_smoke/main.go`. It will use the exact same `SandstoreClient` over gRPC as `open_smoke`, but its execution loop will intersperse gRPC calls with container failure injections.

### The "Crash-Consistency" Test Pattern

Every test in the `durability_smoke` suite must strictly follow this Call ➔ Kill ➔ Assert loop:

**Test Case 1: The Election Amnesia Test**

- **Call:** Client creates `/amnesia_test.txt` and verifies it exists.
- **Kill:** Send `docker kill` to the entire cluster (hard crash, no graceful shutdown).
- **Assert:** Restart the cluster. Wait for leader election. Client calls `Open(/amnesia_test.txt)`. If the nodes suffered amnesia, they might overwrite the log or fail to elect a leader. Expected: `Success`.

**Test Case 2: The Uncommitted Write (Fsync Isolation)**

- **Call:** Client sends `Create(/partial_write.txt)`.
- **Kill:** Using a proxy or tight timing, kill the Leader *immediately* after it receives the RPC but *before* it broadcasts `AppendEntries` to the quorum.
- **Assert:** Restart the node and query the new cluster leader. Expected: `Open(/partial_write.txt)` must fail. The log was never committed, so it must not be applied to the state machine.

**Test Case 3: The Snapshot Integrity Test**

- **Call:** Client loops 10,000 file creations to forcefully exceed `RaftConfig.SnapshotThresholdLogs`.
- **Kill:** Kill a follower node at the exact moment it requests the BoltDB serialization (simulating a crash mid-compaction).
- **Assert:** Restart the follower. Expected: The node must boot successfully using either the old snapshot or the new one. The atomic rename prevents a corrupted state file.

**Test Case 4: The Catch-Up via `InstallSnapshot`**

- **Call:** Pause `node-3` (`docker pause node-3`). Client writes 10,000 files to the leader (`node-1`), forcing the cluster to compact logs up to index 10,000.
- **Kill (Resume):** Unpause `node-3` (`docker unpause node-3`).
- **Assert:** `node-3` attempts to reconnect. The leader realizes `node-3`'s `nextIndex` is missing and sends an `InstallSnapshot` RPC. Wait 5 seconds, then ask `node-3` directly to `Lookup` file #9999. Expected: `Success`. `node-3` safely overwrote its BoltDB file with the snapshot.

---

# Stack

- The entire project is in Go, so this will be in Go as well.

---