package communication

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"sync"
	"time"
)

type HTTPCommunicator struct {
	listenAddress string
	httpServer    *http.Server
	handler       MessageHandler
	clientLock    sync.RWMutex
	clients       map[string]*http.Client
	payloadTypes  map[string]reflect.Type
}

func NewHTTPCommunicator(listenAddress string) *HTTPCommunicator {
	c := &HTTPCommunicator{
		listenAddress: listenAddress,
		clients:       make(map[string]*http.Client),
		payloadTypes:  make(map[string]reflect.Type),
	}

	// Register default payload types
	c.payloadTypes[MessageTypeStoreFile] = reflect.TypeOf((*StoreFileRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeReadFile] = reflect.TypeOf((*ReadFileRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeDeleteFile] = reflect.TypeOf((*DeleteFileRequest)(nil)).Elem()

	return c
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

func mapFromHTTPCode(code int) SandCode {
	switch code {
	case http.StatusOK:
		return CodeOK
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusInternalServerError:
		return CodeInternal
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	default:
		return CodeInternal // Default to internal error for unknown codes
	}
}

func (c *HTTPCommunicator) Send(ctx context.Context, to string, msg Message) (*Response, error) {
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
		return nil, fmt.Errorf("failed to marshal message: %v", err)
	}

	url := fmt.Sprintf("http://%s/message", to)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	headers := map[string]string{}
	for key, values := range resp.Header {
		headers[key] = values[0]
	}

	return &Response{
		Code:    mapFromHTTPCode(resp.StatusCode),
		Body:    respBody,
		Headers: headers,
	}, nil
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

	// First unmarshal to get message type
	var rawMsg struct {
		From    string          `json:"From"`
		Type    string          `json:"Type"`
		Payload json.RawMessage `json:"Payload"`
	}
	if err := json.Unmarshal(body, &rawMsg); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if rawMsg.From == "" || rawMsg.Type == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Create the final message
	msg := Message{
		From: rawMsg.From,
		Type: rawMsg.Type,
	}

	// Deserialize payload based on message type
	if payloadType, exists := c.payloadTypes[rawMsg.Type]; exists {
		if len(rawMsg.Payload) > 0 {
			// Create a new instance of the payload type
			payloadPtr := reflect.New(payloadType).Interface()

			if err := json.Unmarshal(rawMsg.Payload, payloadPtr); err != nil {
				http.Error(w, fmt.Sprintf("Invalid payload for message type %s: %v", rawMsg.Type, err), http.StatusBadRequest)
				return
			}

			// Dereference the pointer to get the actual value
			msg.Payload = reflect.ValueOf(payloadPtr).Elem().Interface()
			log.Printf("Successfully deserialized payload for type %s", rawMsg.Type)
		} else {
			// Empty payload - create zero value
			msg.Payload = reflect.Zero(payloadType).Interface()
		}
	} else {
		log.Printf("No payload type registered for message type: %s", rawMsg.Type)
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
