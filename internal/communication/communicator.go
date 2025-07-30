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
)

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
	FileID string
}

type StopServerRequest struct {
}

type RequestVoteRequest struct {
	Term        int64
	CandidateID string
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
