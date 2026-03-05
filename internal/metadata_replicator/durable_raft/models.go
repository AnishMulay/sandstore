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

// LogStore (WAL): Append-only log persistence
type LogStore interface {
	// Stores a batch of logs. Must fsync before returning.
	StoreLogs(entries []raft_replicator.LogEntry) error

	// Retrieves a specific log by its Raft index
	GetLog(index uint64) (raft_replicator.LogEntry, error)

	// Returns the metadata of the last log in the WAL
	LastIndexAndTerm() (index uint64, term uint64, err error)

	// Deletes logs up to the snapshot index (Log Compaction)
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
