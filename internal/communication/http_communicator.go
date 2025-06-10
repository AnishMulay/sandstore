package communication

type HTTPCommunicator struct {
	Addr       string
	incomingCh chan Message
}

func NewHTTPCommunicator(addr string) *HTTPCommunicator {
	return &HTTPCommunicator{
		Addr:       addr,
		incomingCh: make(chan Message, 100),
	}
}
