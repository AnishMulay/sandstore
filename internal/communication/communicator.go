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
	MessageTypeStoreMetadata  = "store_metadata"
	MessageTypeDeleteMetadata = "delete_metadata"
	MessageTypeStopServer     = "stop_server"
	MessageTypeRequestVote    = "request_vote"
	MessageTypeAppendEntries  = "append_entries"
	// Chunk Replication Messages
	MessageTypeWriteChunk  = "chunk_write" // Replaces "store_chunk"
	MessageTypeReadChunk   = "chunk_read"
	MessageTypeDeleteChunk = "chunk_delete"
)

type WriteChunkRequest struct {
	ChunkID string
	Data    []byte
	// Future-proofing: We could add 'Offset' here later for partial updates!
	// Offset int64
}

type ReadChunkRequest struct {
	ChunkID string
}

type DeleteChunkRequest struct {
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

type StoreMetadataRequest struct {
	Metadata metadata_service.MetadataOperation
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
	CodeOK            SandCode = "OK"
	CodeBadRequest    SandCode = "BAD_REQUEST"
	CodeNotFound      SandCode = "NOT_FOUND"
	CodeAlreadyExists SandCode = "ALREADY_EXISTS"
	CodeInternal      SandCode = "INTERNAL"
	CodeUnavailable   SandCode = "UNAVAILABLE"
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
