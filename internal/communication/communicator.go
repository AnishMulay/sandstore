// communication/interface.go
package communication

import (
	"context"

	"github.com/AnishMulay/sandstore/internal/metadata_service"
)

type Message struct {
	From    string
	Type    string
	Payload any
}

const (
	MessageTypeStoreFile      = "store_file"
	MessageTypeReadFile       = "read_file"
	MessageTypeDeleteFile     = "delete_file"
	MessageTypeStoreChunk     = "store_chunk"
	MessageTypeReadChunk      = "read_chunk"
	MessageTypeDeleteChunk    = "delete_chunk"
	MessageTypeStoreMetadata  = "store_metadata"
	MessageTypeDeleteMetadata = "delete_metadata"
	MessageTypeStopServer     = "stop_server"
	MessageTypeRequestVote    = "request_vote"
	MessageTypeAppendEntries  = "append_entries"
    // POSIX Specific Chunk Messages
    MessageTypePosixWriteChunk  = "posix_write_chunk"  // Replaces "store_chunk"
    MessageTypePosixReadChunk   = "posix_read_chunk"
    MessageTypePosixDeleteChunk = "posix_delete_chunk"
)

type PosixWriteChunkRequest struct {
    ChunkID string
    Data    []byte
    // Future-proofing: We could add 'Offset' here later for partial updates!
    // Offset int64 
}

type PosixReadChunkRequest struct {
    ChunkID string
}

type PosixDeleteChunkRequest struct {
    ChunkID string
}

type StoreFileRequest struct {
	Path string
	Data []byte
}

type ReadFileRequest struct {
	Path string
}

type DeleteFileRequest struct {
	Path string
}

type StoreChunkRequest struct {
	ChunkID string
	Data    []byte
}

type ReadChunkRequest struct {
	ChunkID string
}

type DeleteChunkRequest struct {
	ChunkID string
}

type StoreMetadataRequest struct {
	Metadata metadata_service.FileMetadata
}

type DeleteMetadataRequest struct {
	Path string
}

type StopServerRequest struct {
}

type RequestVoteRequest struct {
	Term         int64
	CandidateID  string
	LastLogIndex int64
	LastLogTerm  int64
}

type AppendEntriesRequest struct {
	Term         int64
	LeaderID     string
	PrevLogIndex int64
	PrevLogTerm  int64
	Entries      []byte // Serialized metadata log entries
	LeaderCommit int64  // Leader's commit index
}

type SandCode string

const (
	CodeOK          SandCode = "OK"
	CodeBadRequest  SandCode = "BAD_REQUEST"
	CodeNotFound    SandCode = "NOT_FOUND"
	CodeInternal    SandCode = "INTERNAL"
	CodeUnavailable SandCode = "UNAVAILABLE"
)

type Response struct {
	Code    SandCode
	Body    []byte
	Headers map[string]string // Optional, used only in HTTP
}

type MessageHandler func(msg Message) (*Response, error)

type Communicator interface {
	Start(handler MessageHandler) error
	Send(ctx context.Context, to string, msg Message) (*Response, error)
	Stop() error
	Address() string
}
