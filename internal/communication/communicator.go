// communication/interface.go
package communication

import "context"

type Message struct {
	From    string
	Type    string
	Payload any
}

const (
	MessageTypeStoreFile   = "store_file"
	MessageTypeReadFile    = "read_file"
	MessageTypeDeleteFile  = "delete_file"
	MessageTypeStoreChunk  = "store_chunk"
	MessageTypeReadChunk   = "read_chunk"
	MessageTypeDeleteChunk = "delete_chunk"
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
