package communication

import (
	"net/http"
	"sync"
)

type HTTPCommunicator struct {
	listenAddress string
	httpServer    *http.Server
	messageChan   chan Message
	clients       map[string]*http.Client
	clientsLock   sync.RWMutex
}
