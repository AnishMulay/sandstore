# New Modern Metadata Service Implementation

---

# Context Block: Sandstore Metadata State Machine Persistence

## 1. The Architectural Scope and Objective

This change introduces a completely new, parallel implementation of the Sandstore `MetadataService` interface. The objective is to transition the metadata state machine (which holds inodes, directory structures, and the superblock) from a volatile, in-memory Go map architecture to a fully durable, ACID-compliant embedded database.

**Crucial Boundary:** This change is strictly isolated to the *State Machine* materialization. It does *not* include the Write-Ahead Log (WAL), the Raft consensus protocol, or the chunk data plane. It is building the persistent foundation upon which the consensus replicator will eventually apply its committed operations. The existing `InMemoryMetadataService` will remain untouched to preserve the system's Open-Closed Principle and fast local testing capabilities.

## 2. The Core Problem: Volatility and POSIX Atomicity

Currently, Sandstore's metadata lives in memory. If the node crashes, the filesystem namespace is completely erased. Beyond simple data loss, the in-memory architecture lacks transactional boundaries. A standard POSIX operation, such as a file `rename` or creating a directory, requires updating multiple data structures simultaneously (e.g., removing the old directory entry, creating a new one, and updating parent inode modification times). In a volatile setup, a crash mid-operation corrupts the filesystem mathematically. We require strict ACID (Atomicity, Consistency, Isolation, Durability) guarantees to ensure that no intermediate or partially applied namespace state is ever leaked or saved to disk.

## 3. Storage Engine Selection: Embedded KV vs. Client-Server

To achieve this, we will use an embedded Key-Value (KV) database. Unlike client-server databases (like PostgreSQL or Redis), an embedded database compiles directly into the Sandstore Go binary and accesses the local disk directly via system calls, avoiding network loopback overhead.

Specifically, this architecture dictates the use of a B+ Tree-based engine (like `go.etcd.io/bbolt`) rather than a Log-Structured Merge (LSM) Tree engine (like `BadgerDB`).

- **Why B+ Tree (`bbolt`):** It provides strict serializable ACID transactions through a single immutable database file mapped into memory. It allows us to wrap complex POSIX mutations inside a single `db.Update(func(tx *bbolt.Tx) error)` closure. If power is lost, the uncommitted transaction is entirely discarded. Furthermore, B+ Trees offer highly predictable read latency without the unpredictable background garbage collection (compaction) spikes inherent to LSM trees, which is a hard requirement for our future benchmarking validity.

## 4. Key-Value Schema Design for a Filesystem

To translate a hierarchical POSIX filesystem into a flat Key-Value store, the database will be divided into logical "buckets" for efficient prefix scanning and point lookups.
The schema must support the following:

- **`superblock` Bucket:** Stores the global filesystem configuration, including the strict 8 MiB chunk size policy and cluster topology.
- **`inodes` Bucket:** Maps an `Inode ID` (uint64) to its serialized metadata (permissions, timestamps, node type).
- **`dentries` (Directory Entries) Bucket:** Maps a composite key of `Parent Inode ID + Entity Name` to a `Child Inode ID`. This composite structure allows for extremely fast, lexicographically sorted directory enumerations (`ReadDir`).
- **`chunk_map` Bucket:** Maps an `Inode ID + Byte Offset` to a serialized `ChunkDescriptor` (ReplicaSet, Generation, AckPolicy) to lay the groundwork for future deterministic data placement.
- **`metadata_state` Bucket:** Crucially, this bucket contains a `consistent_index` key. This stores a `uint64` representing the highest Raft log entry that has been durably applied to the B+ tree. This must be atomically updated alongside every namespace mutation to guarantee exactly-once application logic during future crash recovery sequences.

## 5. Study Session Goals

By the end of the study session utilizing this context, the engineer must deeply understand:

1. How to translate complex POSIX file operations (specifically `Rename` and `Create`) into multi-key ACID transactions within a B+ tree KV store.
2. The structural and behavioral differences between B+ trees and LSM trees, and why B+ trees are superior for benchmark-defensible, read-heavy filesystem metadata.
3. How composite keys (like `dentries`) enable hierarchical path traversal and directory listings in a flat KV architecture.
4. The role of the `consistent_index` in bridging the gap between an independent State Machine and a decoupled Consensus Log (even though the log is not being built in this specific phase).

---

# Process

- [x]  Goals → list out the goals of the project.
- [x]  User stories → user centric design, 10 to 20 user stories for the project.
- [x]  Data models → define the data models and how the data models are going to be interacting with each other.
- [x]  Nail an MVP → what are the user stories which are required for a minimum viable product, get rid of the rest for now.
- [x]  Draw the prototype → paper is cheap, code is expensive.
- [x]  What does the future of the project look like ?
- [x]  Drill into the specific components.
- [ ]  Pick a stack → programming language, frameworks etc.
- [ ]  Actual software development.

---

# Goals

The primary objective of this refactor is to elevate Sandstore's control plane from a volatile prototyping environment to a production-grade, benchmark-defensible architecture. This is a mandatory prerequisite for publishing scientifically rigorous performance benchmarks and a formal white paper.

- **Academic & Benchmarking Defensibility:** Eliminate the "benchmarking crimes" associated with volatile memory. By forcing all metadata mutations through synchronous disk I/O, future benchmarks will accurately measure physical storage constraints and consensus overhead, rather than arbitrary page-cache behavior.
- **Strict POSIX Atomicity (ACID Guarantees):** Transition the namespace state machine to an embedded B+ Tree database (`go.etcd.io/bbolt`). This ensures that complex multi-step POSIX operations (like `Rename` or `Create`) are executed within strict ACID transactional closures, preventing mathematical corruption of the filesystem during power failures.
- **Architectural Modularity (Open-Closed Principle):** Introduce a new `PersistentMetadataService` without breaking or fundamentally altering the existing `MetadataService` Go interface. The existing `inmemory` package must remain fully functional to support high-velocity, low-I/O continuous integration testing.
- **Crash Resiliency & Recovery Foundation:** Introduce the ability for the metadata state machine to durably track its synchronization state (via a `consistent_index`) and expose a recovery mechanism. This lays the groundwork for the node to autonomously reconstruct its namespace after a catastrophic crash.

---

# User Stories

In the context of this backend storage engine, the "users" are the internal File Service executing RPCs, and the Kubernetes/Docker orchestrators managing the node lifecycle. The following stories define the strict ACID constraints and operational behaviors the new `PersistentMetadataService` must guarantee.

### Part 1: ACID Transaction Guarantees (The Write Path)

- **Story 2.1 - Atomic Rename (`OpRename`):** As the File Service, when I issue a rename operation, the underlying B+ tree must execute a single transaction that deletes the source directory entry, inserts the destination directory entry, and updates both parent inodes' modification timestamps. If the node loses power mid-operation, the transaction must be entirely discarded, guaranteeing no "ghost" files or broken links.
- **Story 2.2 - Atomic Creation (`OpCreate` / `Mkdir`):** As the File Service, when I create a new file or directory, the system must atomically write the new child inode to the `inodes` bucket, insert the namespace mapping into the `dentries` bucket, and update the parent directory's modification time.
- **Story 2.3 - Atomic Deletion (`OpRemove` / `Rmdir`):** As the File Service, when I delete an entity, the system must atomically remove the directory entry, decrement the child's link count, and delete the child inode from the database, ensuring no orphaned inodes leak disk space over time.
- **Story 2.4 - State Machine Synchronization (`consistent_index`):** As the Consensus Replicator, whenever I apply any of the above mutating operations, the B+ tree transaction must *also* overwrite the `consistent_index` key in the `metadata_state` bucket. This guarantees the filesystem state and the consensus applied-index are permanently locked together on disk.

### Part 2: Deterministic Reads (The Read Path)

- **Story 2.5 - Lock-Free Snapshot Reads:** As a client issuing read-only operations (`Lookup`, `GetAttributes`, `GetFsStat`), I want my queries to execute against an immutable, consistent snapshot of the B+ tree. A concurrent `OpRename` transaction must not block my read or show me a partially mutated directory state.
- **Story 2.6 - Lexicographical Directory Enumeration:** As a client calling `ReadDir`, I want the returned list of files to be strictly deterministic and alphabetically sorted. The system will achieve this by performing a prefix-scan on the composite keys within the `dentries` bucket, entirely replacing the unpredictable ordering of Go map iteration.

### Part 3: Lifecycle & Crash Recovery (The Operational Path)

- **Story 2.7 - Autonomous Crash Recovery (`Recover`):** As a Kubernetes cluster operator, if a Sandstore pod crashes and restarts, the node must automatically open its local `state.db` file, read its `consistent_index`, and fully reconstruct its state *before* `comm.Start()` opens the TCP port (8080). This ensures the pod does not pass its readiness probe or accept traffic until its namespace is fully intact.
- **Story 2.8 - First-Time Bootstrap:** As a cluster operator deploying a brand new cluster, if `MetadataService.Start()` initializes and finds an empty database, it must execute a single local transaction to create the default `superblock` and root inode before starting the Raft replicator.
- **Story 2.9 - The Open-Closed Principle:** As a core developer writing unit tests, I want the ability to run the test suite using the untouched `InMemoryMetadataService`. The new persistent logic must be completely decoupled, allowing me to bypass disk I/O penalties when I am not actively benchmarking durability.

---

# Data Models

To translate the hierarchical POSIX filesystem into a flat, ACID-compliant B+ Tree (`bbolt`), the database is partitioned into five distinct logical buckets. Keys and values will be serialized into byte slices (gob encoding for the values and binary byte encoding for the keys, we use binary big endian byte encoding for the keys because we do not want a key like 10 to be before a key like 2 in the sort order because bbolt sorts lexicographically) before being persisted.

- **Bucket: `metadata_state` (The Consensus Bridge)**
    - **Key:** `[]byte("consistent_index")`
    - **Value:** `uint64` (Little Endian encoded)
    - **Description:** The authoritative synchronization marker. It stores the exact index of the last Raft log entry durably applied to the B+ tree. This is the bedrock of the `Recover()` function, ensuring exactly-once application logic.
- **Bucket: `superblock` (Filesystem Configuration)**
    - **Key:** `[]byte("sb")`
    - **Value:** Serialized `Superblock` Struct (Root Inode ID, Allocator State, Version, Cluster Topology, Global Chunk Size).
    - **Description:** Contains global configuration parameters that apply to the entire filesystem instance.
- **Bucket: `inodes` (File/Directory Metadata)**
    - **Key:** `uint64` (Inode ID, Big Endian encoded for proper sorting if needed)
    - **Value:** Serialized `Inode` Struct (Mode, Link Count, Size, Owner/Group IDs, Access/Modify/Change timestamps).
    - **Description:** The primary storage for entity metadata. Notice that `Children` maps and `ChunkLists` are explicitly *excluded* from this struct to prevent massive serialization overheads on single-byte file appends.
- **Bucket: `dentries` (Directory Entries & Hierarchy)**
    - **Key:** `uint64` (Parent Inode ID) + `[]byte(Entity Name)`
    - **Value:** `uint64` (Child Inode ID)
    - **Description:** The hierarchical mapping structure. Because bbolt stores keys in byte-sorted order, a `ReadDir` operation simply requires opening a cursor and seeking to the prefix of the `Parent Inode ID`. This yields a deterministic, alphabetically sorted list of directory contents.
- **Bucket: `chunk_map` (Data Placement Pointers)**
    - **Key:** `uint64` (File Inode ID) + `uint64` (Chunk Index / Logical Offset)
    - **Value:** Serialized `ChunkDescriptor` Struct (Chunk ID, ReplicaSet Hint, Generation Number).
    - **Description:** Maps logical file offsets to physical chunk data. Structuring the value as a `ChunkDescriptor` rather than a raw ID future-proofs the state machine for Phase 3 (Deterministic Chunk Placement).

---

# MVP

The MVP for this phase is strictly defined by the User Stories outlined in Section 2. There are no optional features or "nice-to-haves" in this iteration; every defined story is structurally load-bearing.

- **Included in MVP:** Full implementation of the 5 mutating POSIX intents (`OpCreate`, `OpRemove`, `OpRename`, `OpSetAttr`, `OpUpdateInode`) wrapped in `bbolt` ACID transactions, deterministic prefix-scanned reads, and the `Recover()` bootstrap sequence.
- **Excluded from MVP:** The durable Raft Write-Ahead Log (WAL), background garbage collection, and dynamic cluster reconfiguration. The scope is strictly isolated to State Machine materialization.

---

# Project Future

Implementing this durable B+ Tree state machine serves as the bedrock for the overarching goal of the Sandstore project: producing a rigorously benchmarked, publishable distributed systems white paper. The immediate and long-term roadmap includes:

- **Phase 2: Consensus Durability (Persistent Raft):** The immediate next step is building the `PersistentRaftReplicator` backed by a Write-Ahead Log (WAL). This will solve the "amnesia" edge case, allowing the consensus engine to perfectly sync with this durable state machine upon node reboot.
- **Storage Engine Matrix (LSM vs. B+ Tree):** By defining this clean boundary for the state machine, we unlock the ability to hot-swap embedded databases. Since 90% of modern databases use either B+ Trees or Log-Structured Merge (LSM) Trees, a future project will implement a `RocksDB` or `BadgerDB` (LSM) version of this service. This will allow us to directly benchmark write-amplification and read-latency tradeoffs between the two dominant storage paradigms under identical distributed POSIX workloads.
- **Asymmetric Cluster Topologies:** Currently, Sandstore runs homogenous nodes (every node runs all services). With the state machine decoupled and durable, we can configure asymmetrical topologies—such as a GFS/HDFS-style architecture with dedicated, highly-available Metadata servers and separate, high-capacity Chunk servers.

---

# Component Flows

The following flows define exactly how the `BoltMetadataService` will service the replicated intents passed down from the consensus engine. Every mutating operation is wrapped in a `db.Update()` closure to guarantee strict ACID semantics. Keys representing `InodeID`s are encoded using `binary.BigEndian` to preserve lexicographical sorting, while values are serialized using `encoding/gob`.

### Flow 7.1: `ApplyCreate` (Atomic File/Directory Creation)

*When the Raft replicator commits an `OpCreate`, the state machine executes:*

1. **Begin Transaction:** Open `db.Update(func(tx *bbolt.Tx) error)`.
2. **Fetch Buckets:** Get the `inodes`, `dentries`, and `metadata_state` buckets.
3. **Pre-Flight Checks:**
    - Read the parent inode from the `inodes` bucket. If it doesn't exist, rollback (`return ErrNotFound`).
    - Check the `dentries` bucket for the composite key `[Parent Inode ID bytes] + [Name bytes]`. If a value exists, rollback (`return ErrAlreadyExists`).
4. **Create Child Inode:**
    - Instantiate the new `Inode` struct in Go (copying Mode, UID, GID, and timestamps from the operation).
    - *Crucial:* Leave `Children` and `ChunkList` nil/empty.
    - Serialize the `Inode` using `gob`.
    - Put it into the `inodes` bucket using `[Child Inode ID bytes]` as the key.
5. **Create Directory Entry:**
    - Put the mapping into the `dentries` bucket. Key: `[Parent Inode ID bytes] + [Name bytes]`, Value: `[Child Inode ID bytes]`.
6. **Update Parent Inode:**
    - Update the parent inode's `ModifyTime` and `ChangeTime`. If the child is a directory, increment the parent's `LinkCount`.
    - Re-serialize the parent `Inode` and overwrite it in the `inodes` bucket.
7. **Commit State:** Overwrite the `consistent_index` in the `metadata_state` bucket with the operation's Raft index.
8. **End Transaction:** Return `nil` to automatically commit the transaction to disk.

### Flow 7.2: `ApplyRename` (Atomic Namespace Move)

*When the Raft replicator commits an `OpRename`, the state machine executes:*

1. **Begin Transaction:** Open `db.Update(func(tx *bbolt.Tx) error)`.
2. **Fetch Buckets:** Get the `inodes`, `dentries`, and `metadata_state` buckets.
3. **Pre-Flight Checks:**
    - Read the `srcParent` and `dstParent` from the `inodes` bucket. Rollback if missing.
    - Look up the target child in `dentries` using `[Source Parent ID bytes] + [Source Name bytes]`. If missing, rollback.
4. **Delete Source Entry:**
    - Call `Delete()` on the `dentries` bucket for the source composite key.
5. **Handle Overwrites (If applicable):**
    - Check `dentries` for `[Destination Parent ID bytes] + [Destination Name bytes]`.
    - If an old inode exists there, delete it from the `inodes` bucket to prevent orphans.
6. **Insert Destination Entry:**
    - Put the moved child into `dentries` with Key: `[Destination Parent ID bytes] + [Destination Name bytes]`, Value: `[Child Inode ID bytes]`.
7. **Update Parents:**
    - Update `ModifyTime` and `ChangeTime` on both `srcParent` and `dstParent` structs, re-serialize them, and overwrite them in the `inodes` bucket.
8. **Commit State:** Overwrite the `consistent_index` and return `nil` to commit.

### Flow 7.3: `ApplyRemove` (Atomic Deletion)

*When the Raft replicator commits an `OpRemove`, the state machine executes:*

1. **Begin Transaction:** Open `db.Update(func(tx *bbolt.Tx) error)`.
2. **Fetch Buckets:** Get the `inodes`, `dentries`, and `metadata_state` buckets.
3. **Locate Target:**
    - Look up the child ID in `dentries` using `[Parent ID bytes] + [Name bytes]`.
4. **Delete Entities:**
    - Call `Delete()` on the `dentries` bucket to remove the namespace mapping.
    - Call `Delete()` on the `inodes` bucket using the child ID to erase the file metadata.
5. **Update Parent:**
    - Decrement the parent's `LinkCount` (if the removed child was a directory). Update timestamps. Re-serialize and save to `inodes`.
6. **Commit State:** Overwrite the `consistent_index` and return `nil` to commit.

### Flow 7.4: `ReadDir` (Deterministic Enumeration)

*Unlike mutations, this is a read-only client query that does not modify state.*

1. **Begin Transaction:** Open `db.View(func(tx *bbolt.Tx) error)` (Read-Only).
2. **Fetch Bucket:** Get the `dentries` bucket.
3. **Prefix Scan:**
    - Open a Cursor on the `dentries` bucket.
    - `Seek()` to the byte prefix representing the `[Parent Inode ID bytes]`.
    - Iterate forward. Because the keys are structured as `[Parent ID] + [Name]`, bbolt will naturally return them in perfect alphabetical order.
    - For each key that matches the parent prefix, extract the `[Name]`, read the corresponding child from the `inodes` bucket to get its `Type`, and append it to the results slice.
4. **End Transaction:** Return the slice.

### Flow 7.5: `Recover` (The Boot Sequence)

*Executed by `MetadataService.Start()` before network traffic is accepted.*

1. **Open Database:** Initialize the `bbolt` DB file at `DATA_DIR/state.db`.
2. **Begin Transaction:** Open `db.View(func(tx *bbolt.Tx) error)`.
3. **Read State:** * Check the `metadata_state` bucket for `consistent_index`.
    - If the bucket is empty/missing, initialize the DB with a `db.Update` (creating the root inode and superblock buckets) and return `consistent_index = 0`.
    - If it exists, read the `uint64` value.
4. **Return State:** The node is successfully recovered up to `consistent_index`. (In Phase 2, the Raft WAL will use this index to replay missed entries).

## Flow 7.6: Lock-Free Snapshot Reads (Lookup, GetAttributes, GetFsStat)
*These operations provide strongly consistent, non-blocking reads against a point-in-time snapshot of the database.*
1. *Begin Transaction:* Open `db.View(func(tx *bbolt.Tx) error)` (Read-Only).
2. *Execute Query:*
   * *For `Lookup`:* Fetch `inodes` to verify the parent is a directory, then fetch `dentries` using `[Parent ID bytes] + [Name bytes]` to return the child Inode ID.
   * *For `GetAttributes`:* Fetch `inodes` using `[Inode ID bytes]`, deserialize with `gob`, and return the required fields (Mode, Size, timestamps).
   * *For `GetFsStat`:* Fetch `superblock` to return global limits, and use `tx.Bucket([]byte("inodes")).Stats().KeyN` to instantly return the total file count.
3. *End Transaction:* Return the requested data. (No locks block concurrent `db.Update` transactions).

---