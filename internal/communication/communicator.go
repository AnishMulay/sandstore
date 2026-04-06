// communication/interface.go
package communication

import (
	"context"
)

type Message struct {
	From    string
	Type    string
	Payload any
}

const (
	MessageTypeStoreFile  = "store_file"
	MessageTypeReadFile   = "read_file"
	MessageTypeDeleteFile = "delete_file"
	MessageTypeStoreChunk = "store_chunk"
	MessageTypeStopServer = "stop_server"
	// Chunk Replication Messages
	MessageTypeReadChunk    = "chunk_read"
	MessageTypeDeleteChunk  = "chunk_delete"
	MessageTypePrepareChunk = "chunk_prepare"
	MessageTypeCommitChunk  = "chunk_commit"
	MessageTypeAbortChunk   = "chunk_abort"
)

type ReadChunkRequest struct {
	ChunkID string
}

type DeleteChunkRequest struct {
	ChunkID string
}

type PrepareChunkRequest struct {
	TxnID    string
	ChunkID  string
	Data     []byte
	Checksum string
}

type CommitChunkRequest struct {
	TxnID   string
	ChunkID string
}

type AbortChunkRequest struct {
	TxnID   string
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

type StopServerRequest struct {
}

type SandCode string

const (
	CodeOK            SandCode = "OK"
	CodeBadRequest    SandCode = "BAD_REQUEST"
	CodeNotFound      SandCode = "NOT_FOUND"
	CodeAlreadyExists SandCode = "ALREADY_EXISTS"
	CodeNotLeader     SandCode = "NOT_LEADER"
	CodeInternal      SandCode = "INTERNAL"
	CodeUnavailable   SandCode = "UNAVAILABLE"
)

type Response struct {
	Code    SandCode
	Body    []byte
	Headers map[string]string // Optional, used only in HTTP
}

type MessageHandler func(ctx context.Context, msg Message) (*Response, error)

type Communicator interface {
	Start(handler MessageHandler) error
	Send(ctx context.Context, to string, msg Message) (*Response, error)
	Stop() error
	Address() string
}
