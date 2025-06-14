// communication/interface.go
package communication

import "context"

type Message struct {
	From    string
	Type    string
	Payload []byte
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
	Send(ctx context.Context, to string, msg Message) error
	Stop() error
	Address() string
}
