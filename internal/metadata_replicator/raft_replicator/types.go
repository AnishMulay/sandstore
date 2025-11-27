package raft_replicator

import (
	"fmt"
	"time"
)

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
	Term      int64
	Success   bool
	ConflictIndex int64
	ConflictTerm  int64
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