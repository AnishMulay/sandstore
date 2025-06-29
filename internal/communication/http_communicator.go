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
	handler       MessageHandler
	clientLock    sync.RWMutex
	clients       map[string]*http.Client
}

func NewHTTPCommunicator(listenAddress string) *HTTPCommunicator {
	return &HTTPCommunicator{
		listenAddress: listenAddress,
		clients:       make(map[string]*http.Client),
	}
}

func (c *HTTPCommunicator) Address() string {
	return c.listenAddress
}

func (c *HTTPCommunicator) Start(handler MessageHandler) error {
	c.handler = handler

	mux := http.NewServeMux()
	mux.HandleFunc("/message", c.handleHTTPMessage)

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

func (c *HTTPCommunicator) Send(ctx context.Context, to string, msg Message) error {
	c.clientLock.RLock()
	client, ok := c.clients[to]
	c.clientLock.RUnlock()

	if !ok {
		client = &http.Client{
			Timeout: 5 * time.Second,
		}
		c.clientLock.Lock()
		c.clients[to] = client
		c.clientLock.Unlock()
	}

	msg.From = c.listenAddress
	jsonData, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s/message", to)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
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
		return fmt.Errorf("failed to send message: %s", string(body))
	}

	// Process the response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	// If there's a response body, try to parse it as a Message and handle it
	if len(respBody) > 0 {
		var respMsg Message
		if err := json.Unmarshal(respBody, &respMsg); err == nil {
			// If we successfully parsed a Message, call the handler
			if c.handler != nil {
				c.handler(respMsg)
			}
		}
	}

	return nil
}

func mapToHTTPCode(code SandCode) int {
	switch code {
	case CodeOK:
		return http.StatusOK
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeNotFound:
		return http.StatusNotFound
	case CodeInternal:
		return http.StatusInternalServerError
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (c *HTTPCommunicator) handleHTTPMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if msg.From == "" || msg.Type == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	if c.handler == nil {
		http.Error(w, "Handler not set", http.StatusInternalServerError)
		return
	}

	resp, err := c.handler(msg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Handler error: %v", err), http.StatusInternalServerError)
		return
	}

	if resp != nil {
		// Set custom headers if any (must be done before WriteHeader)
		if resp.Headers != nil {
			for key, value := range resp.Headers {
				w.Header().Set(key, value)
			}
		}

		// Set HTTP status code based on SandCode
		httpStatus := mapToHTTPCode(resp.Code)
		w.WriteHeader(httpStatus)

		// Write body directly to response
		if resp.Body != nil {
			w.Write(resp.Body)
		}

		log.Printf("Sent response to %s with code: %s", msg.From, resp.Code)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}
