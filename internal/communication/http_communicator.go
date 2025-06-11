package communication

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
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

func (c *HTTPCommunicator) Address() string {
	return c.listenAddress
}

func (c *HTTPCommunicator) Receive(ctx context.Context) (Message, error) {
	select {
	case msg := <-c.messageChan:
		return msg, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (c *HTTPCommunicator) Send(ctx context.Context, to string, msgType string, payload []byte) error {
	c.clientsLock.RLock()
	client, ok := c.clients[to]
	c.clientsLock.RUnlock()

	if !ok {
		client = &http.Client{
			Timeout: 5 * time.Second,
		}
		c.clientsLock.Lock()
		c.clients[to] = client
		c.clientsLock.Unlock()
	}

	msg := Message{
		From:    c.listenAddress,
		Type:    msgType,
		Payload: payload,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://%s/message", to), bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send message: %s", body)
	}

	return nil
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

func (c *HTTPCommunicator) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.httpServer.Shutdown(ctx)
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
