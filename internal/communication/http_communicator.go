package communication

import (
	"encoding/json"
	"io"
	"log"
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

func NewHTTPCommunicator(listenAddress string) *HTTPCommunicator {
	return &HTTPCommunicator{
		listenAddress: listenAddress,
		messageChan:   make(chan Message),
		clients:       make(map[string]*http.Client),
	}
}

func (c *HTTPCommunicator) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/message", c.handleMessage)

	c.httpServer = &http.Server{
		Addr:    c.listenAddress,
		Handler: mux,
	}

	go func() {
		if err := c.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	return nil
}

func (c *HTTPCommunicator) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
	}

	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "Invalid message format", http.StatusBadRequest)
		return
	}

	if msg.From == "" || msg.Type == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	select {
	case c.messageChan <- msg:
	default:
		http.Error(w, "Message channel is full", http.StatusServiceUnavailable)
	}
}
