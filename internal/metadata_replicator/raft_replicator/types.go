package raft_replicator

import (
	"fmt"
	"time"

	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
)

type MetadataOperation = pms.MetadataOperation

// RaftState captures the current role of the node
type RaftState int

const (
	Follower RaftState = iota
	Candidate
	Leader
)

func (s RaftState) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// LogEntry represents a single replicated state machine command
type LogEntry struct {
	Index     int64
	Term      int64
	Data      []byte
	Timestamp time.Time
}

// To ensure atomic updates to both the Intent Log and Metadata, the Raft
// State Machine decodes a unified envelope.
type PayloadType int

const (
	PayloadPosixMeta   PayloadType = 1
	PayloadChunkIntent PayloadType = 2
)

type RaftCommandEnvelope struct {
	Type        PayloadType
	PosixMeta   *MetadataOperation    // Existing POSIX metadata operations
	ChunkIntent *ChunkIntentOperation // New 2PC Intent operations
}

// ChunkIntentOperation replaces ChunkCommitOperation to allow explicit Aborts.
// Proposed to Raft ONLY by the ReplicationCoordinator.
type ChunkIntentOperation struct {
	TxnID     string
	ChunkID   string
	NodeIDs   []string    // The physical placement mapping
	State     IntentState // Usually StateCommitted, rarely StateAborted
	Timestamp int64
}

// Stored in the shared Raft bbolt database.
type IntentState int

const (
	StateUnknown   IntentState = 0
	StatePrepared  IntentState = 1 // Rarely stored in Raft, mostly implied by disk
	StateCommitted IntentState = 2
	StateAborted   IntentState = 3
)

// --- RPC Request/Response Structs ---

type RequestVoteArgs struct {
	Term         int64
	CandidateID  string
	LastLogIndex int64
	LastLogTerm  int64
}

type RequestVoteReply struct {
	Term        int64
	VoteGranted bool
}

type AppendEntriesArgs struct {
	Term         int64
	LeaderID     string
	PrevLogIndex int64
	PrevLogTerm  int64
	Entries      []LogEntry
	LeaderCommit int64
}

type AppendEntriesReply struct {
	Term          int64
	Success       bool
	ConflictIndex int64
	ConflictTerm  int64
}

type InstallSnapshotReply struct {
	Term    int64
	Success bool
}

// --- Errors ---

// ErrNotLeader is returned by Replicate when the node is not the leader.
// It contains hints for redirection.
type ErrNotLeader struct {
	LeaderID   string
	LeaderAddr string
}

func (e ErrNotLeader) Error() string {
	return fmt.Sprintf("node is not the leader. leader is %s at %s", e.LeaderID, e.LeaderAddr)
}
