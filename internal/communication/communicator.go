package communication

type Message struct {
	From    string
	Type    string
	Payload []byte
}

type Communicator interface {
	Start() error
	Send(to string, message Message) error
	Receive() (<-chan Message, error)
	Stop() error
}
