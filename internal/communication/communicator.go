package communication

import "context"

type Message struct {
	From    string
	Type    string
	Payload []byte
}

type Communicator interface {
	Start() error
	Receive(ctx context.Context) (Message, error)
	Send(ctx context.Context, to string, msgType string, payload []byte) error
	Stop() error
	Address() string
}
