# POSIX Chunk Replicator Design

## Process

- [ ] **Goals** → Define the transport responsibilities.
- [ ] **User stories** → "Durability" and "Availability" stories.
- [ ] **Data models** → Placement Groups and Replica Sets.
- [ ] **Nail an MVP** → Simple Fan-Out (Primary-Backup) replication.
- [ ] **Draw the prototype** → Visualizing Fan-Out vs. Chain.
- [ ] **Future** → Chain Replication, Rack Awareness, Rebalancing.
- [ ] **Drill into specific components** → Interface definitions.
- [ ] **Pick a stack** → Go + Communicator.
- [ ] **Actual software development**.

## Goals

### Vision and Purpose

The `PosixChunkReplicator` ensures that data written to the `ChunkService` is copied to the correct set of physical nodes.
It abstracts the topology: The `ChunkService` just says "Replicate this", and the Replicator handles the networking, retries, and node selection.

### Functional Goals

-   **Durability**: Ensure data is written to `$N$ replicas (e.g., 3) before acknowledging success.
-   **Placement Strategy**: Determine which nodes should hold the data (e.g., via Hashing or a Map).
-   **Failure Handling**: If a replica is down, the write must either fail (strong consistency) or pick a temporary handoff (eventual consistency). For MVP, we fail (Strict).

### Non-Functional Goals

-   **Parallelism**: When using Fan-Out, requests to replicas should happen in parallel, not sequentially.
-   **Bandwidth Efficiency**: Future designs (Chain) should optimize for pipeline throughput.

## User Stories (Logistics Focus)

-   **Write Fan-Out**: As the Replicator (Primary), when I receive a `Replicate` call, I want to send the data to Node A and Node B simultaneously and wait for both to ACK, maximizing speed.
-   **Node Resolution**: As the Replicator, I need to consult a `PlacementStrategy` to know that "Chunk #99" belongs on "Node 1, Node 2, Node 3".
-   **Self-Protection**: As the Replicator, if I am asked to replicate a chunk to myself, I want to call `ChunkService.WriteLocal` directly to avoid a network loop.
-   **Chain Support (Future)**: As the Replicator (Head), I want to forward data only to the `Next` node in the chain and wait for the tail to acknowledge.

## Data Models

### 1. Placement Group (PG)

A logical mapping of a chunk to its physical replicas.

```go
type PlacementGroup struct {
    ChunkID string
    Peers   []string // List of Node Addresses (e.g., ["10.0.0.1:8080", "10.0.0.2:8080"])
}
```

### 2. Replication Policy

Configuration for the cluster.

```go
type Policy struct {
    ReplicationFactor int  // e.g., 3
    Strategy          Enum // FanOut (MVP) or Chain (Future)
}
```

## MVP

The MVP uses Primary-Backup (Fan-Out) with a Static Placement Strategy.

**Logic**:

1.  Receive `Replicate(chunkID, data)`.
2.  Calculate hash: `hash(chunkID) % num_nodes` to find the "Primary" and the next `$N-1$` nodes as replicas.
3.  Loop over the target peers:
    -   If `Peer == Self`: Write to Local Disk.
    -   If `Peer != Self`: Send `WriteChunk` RPC via Communicator.
4.  Wait for `All` to succeed (W=All) or Quorum (W=Majority). MVP: Wait for `All`.

## Prototyping (Topology Patterns)

### 1. Fan-Out (The MVP Pattern)

Best for Latency (Fastest Response Time).

```
      [Client/Replicator]
          /    |    \
         /     |     \
    [Node A] [Node B] [Node C]
```

Replicator sends to A, B, C at the same time.
`Latency = max(latency_A, latency_B, latency_C)`.

### 2. Chain Replication (The Future Pattern)

Best for Throughput (High Bandwidth).

```
[Client] -> [Node A] -> [Node B] -> [Node C] -> [Client]
```

Client sends to A. A pipes to B. B pipes to C.
C (Tail) sends Ack to Client (or back up the chain).
This allows the client to saturate the network link to A without being bottlenecked by the others.

## Project Future

### Future Extensions

-   **Chain Replication**: Change the implementation of `Replicate` to forward to `Successor(Node)` instead of broadcasting.
-   **Bit-Rot Detection**: The Replicator periodically scrubs other replicas to compare checksums.
-   **Rebalancing**: If a new node joins, the Replicator (background worker) moves chunks to the new node to balance the load.

## Specific Components

### 1. Interface Definitions

`sandstore/internal/posix_chunk_replicator/chunk_replicator.go`

```go
package posixchunkreplicator

import "context"

type PosixChunkReplicator interface {
    // Replicate ensures the data is stored on the correct set of nodes.
    // This is called by the ChunkService when a write occurs.
    Replicate(ctx context.Context, chunkID string, offset int64, data []byte) error
    
    // Delete ensures the chunk is removed from all replicas.
    Delete(ctx context.Context, chunkID string) error
}

// PlacementStrategy decides WHO gets the data.
// Separating this makes it easy to switch from Static -> Consistent Hashing later.
type PlacementStrategy interface {
    GetReplicas(chunkID string) ([]string, error)
}
```

### 2. Implementation (Fan-Out / Primary-Backup)

`sandstore/internal/posix_chunk_replicator/fanout/replicator.go`

```go
type FanOutReplicator struct {
    communicator    communication.Communicator
    clusterService  cluster.ClusterService // Existing service to get node list
    localNodeID     string
    
    // Dependencies needed to write locally
    // We use an interface to avoid circular imports with ChunkService
    localWriter     LocalWriter 
}

// LocalWriter matches the signature of ChunkService.WriteLocal
type LocalWriter interface {
    WriteLocal(ctx context.Context, id string, offset int64, data []byte) error
}

func (r *FanOutReplicator) Replicate(ctx, id, offset, data) error {
    // 1. Where should this go?
    targets := r.getReplicas(id)
    
    // 2. Create Error Channel (Wait for all)
    errCh := make(chan error, len(targets))
    
    // 3. Dispatch in Parallel
    for _, node := range targets {
        go func(target string) {
            if target == r.localNodeID {
                // Write to disk directly
                errCh <- r.localWriter.WriteLocal(ctx, id, offset, data)
            } else {
                // Send RPC over network
                msg := &comm.Message{
                    Type: MsgWriteChunk,
                    Payload: Serialize(id, offset, data),
                }
                _, err := r.communicator.Send(target, msg)
                errCh <- err
            }
        }(node)
    }
    
    // 4. Wait for results (Barrier)
    for i := 0; i < len(targets); i++ {
        if err := <-errCh; err != nil {
            return err // Fail fast or collect errors
        }
    }
    
    return nil
}
```

## Stack

-   **Language**: Go
-   **Concurrency**: `sync.WaitGroup` or Channels for the fan-out barrier.
-   **Hashing**: `crc32` or `fnv` for simple modulo hashing in the MVP placement strategy.
