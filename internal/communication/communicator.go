// communication/interface.go
package communication

import "context"

type Message struct {
	From    string
	Type    string
	Payload []byte
}

type MessageHandler func(msg Message) (*Message, error)

type Communicator interface {
	Start(handler MessageHandler) error
	Send(ctx context.Context, to string, msg Message) error
	Stop() error
	Address() string
}
