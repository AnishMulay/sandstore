package protocol

import pms "github.com/AnishMulay/sandstore/internal/metadata_service"

const (
	// MessageTypeStoreMetadata identifies a replicated metadata mutation request.
	MessageTypeStoreMetadata = "store_metadata"
	// MessageTypeDeleteMetadata identifies a replicated metadata delete request.
	MessageTypeDeleteMetadata = "delete_metadata"
	// MessageTypeRequestVote identifies a Raft RequestVote RPC.
	MessageTypeRequestVote = "request_vote"
	// MessageTypeAppendEntries identifies a Raft AppendEntries RPC.
	MessageTypeAppendEntries = "append_entries"
	// MessageTypeInstallSnapshot identifies a Raft InstallSnapshot RPC.
	MessageTypeInstallSnapshot = "install_snapshot"
)

// StoreMetadataRequest contains a metadata mutation that should be replicated.
type StoreMetadataRequest struct {
	Metadata pms.MetadataOperation
}

// DeleteMetadataRequest identifies metadata that should be deleted.
type DeleteMetadataRequest struct {
	Path string
}

// RequestVoteRequest carries a Raft RequestVote RPC payload.
type RequestVoteRequest struct {
	Term         int64
	CandidateID  string
	LastLogIndex int64
	LastLogTerm  int64
}

// AppendEntriesRequest carries a Raft AppendEntries RPC payload.
type AppendEntriesRequest struct {
	Term         int64
	LeaderID     string
	PrevLogIndex int64
	PrevLogTerm  int64
	Entries      []byte
	LeaderCommit int64
}

// InstallSnapshotRequest carries a Raft InstallSnapshot RPC payload.
type InstallSnapshotRequest struct {
	Term              int64
	LeaderID          string
	LastIncludedIndex int64
	LastIncludedTerm  int64
	Data              []byte
}
