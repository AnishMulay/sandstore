package communication

type Message struct {
	From    string
	Type    string
	Payload []byte
}
