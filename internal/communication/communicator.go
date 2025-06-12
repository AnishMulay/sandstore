package communication

import "context"

type Message struct {
	From    string
	Type    string
	Payload []byte
}

type Communicator interface {
	Start() error
	ReceiveSync(ctx context.Context) (Message, error)
	SendSync(ctx context.Context, to string, msgType string, payload []byte) error
	SendAsync(ctx context.Context, to string, msgType string, payload []byte) error
	ReceiveAsync(ctx context.Context) (Message, error)
	Stop() error
	Address() string
}
