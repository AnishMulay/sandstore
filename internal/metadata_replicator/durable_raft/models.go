package durable_raft

import (
	"time"

	"github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
)

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

// StableStore persists the voting and term state.
// Must guarantee synchronous durability (fsync) on every write.
type StableStore interface {
	SetState(currentTerm uint64, votedFor string) error
	GetState() (currentTerm uint64, votedFor string, err error)
}

// LogStore (WAL): Append-only log persistence.
type LogStore interface {
	// StoreLogs persists a batch of entries to stable storage.
	// Implementations MUST complete all of the following before returning nil:
	//   1. Marshal entries and wrap in a walEnvelope with a CRC32 checksum.
	//   2. Write the envelope bytes to a temporary file (.tmp suffix).
	//   3. Call Sync() on the temporary file descriptor.
	//   4. Close the temporary file descriptor.
	//   5. Call os.Rename to atomically replace the final path.
	//   6. Open the parent directory and call Sync() on it.
	// A nil return is a durable append guarantee.
	// Any error in steps 1-6 must be returned to the caller.
	StoreLogs(entries []raft_replicator.LogEntry) error

	// GetLog retrieves the entry at the given Raft index.
	// Returns ErrLogCompacted if the index has been compacted.
	// Returns ErrLogNotFound if the index was never written.
	GetLog(index uint64) (raft_replicator.LogEntry, error)

	// LastIndexAndTerm returns the index and term of the last log entry.
	// Returns (0, 0, nil) if the log is empty.
	LastIndexAndTerm() (index uint64, term uint64, err error)

	// DeleteRange compacts the log by deleting entries in [min, max] inclusive.
	DeleteRange(min uint64, max uint64) error
}

// SnapshotMeta contains metadata for a state machine snapshot
type SnapshotMeta struct {
	LastIncludedIndex uint64
	LastIncludedTerm  uint64
}

// SnapshotStore handles saving the opaque state machine state to disk
type SnapshotStore interface {
	SaveSnapshot(meta SnapshotMeta, stateMachineData []byte) error
	LoadSnapshot() (meta SnapshotMeta, stateMachineData []byte, err error)
}
