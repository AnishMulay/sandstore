package communication

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/log_service"
)

type HTTPCommunicator struct {
	listenAddress string
	httpServer    *http.Server
	handler       MessageHandler
	ls            log_service.LogService
	clientLock    sync.RWMutex
	clients       map[string]*http.Client
	payloadTypes  map[string]reflect.Type
}

func NewHTTPCommunicator(listenAddress string, ls log_service.LogService) *HTTPCommunicator {
	c := &HTTPCommunicator{
		listenAddress: listenAddress,
		ls:            ls,
		clients:       make(map[string]*http.Client),
		payloadTypes:  make(map[string]reflect.Type),
	}

	// Register default payload types
	c.payloadTypes[MessageTypeStoreFile] = reflect.TypeOf((*StoreFileRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeReadFile] = reflect.TypeOf((*ReadFileRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeDeleteFile] = reflect.TypeOf((*DeleteFileRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeStoreChunk] = reflect.TypeOf((*StoreChunkRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeReadChunk] = reflect.TypeOf((*ReadChunkRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeDeleteChunk] = reflect.TypeOf((*DeleteChunkRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeStoreMetadata] = reflect.TypeOf((*StoreMetadataRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeDeleteMetadata] = reflect.TypeOf((*DeleteMetadataRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeStopServer] = reflect.TypeOf((*StopServer)(nil)).Elem()

	return c
}

func (c *HTTPCommunicator) Address() string {
	return c.listenAddress
}

func (c *HTTPCommunicator) Start(handler MessageHandler) error {
	c.ls.Info(log_service.LogEvent{
		Message:  "Starting HTTP communicator",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	c.handler = handler

	mux := http.NewServeMux()
	mux.HandleFunc("/message", c.handleHTTPMessage)

	c.httpServer = &http.Server{
		Addr:    c.listenAddress,
		Handler: mux,
	}

	c.ls.Info(log_service.LogEvent{
		Message:  "HTTP communicator started successfully",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	go func() {
		if err := c.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			c.ls.Error(log_service.LogEvent{
				Message:  "HTTP server error",
				Metadata: map[string]any{"address": c.listenAddress, "error": err.Error()},
			})
		}
	}()

	return nil
}

func (c *HTTPCommunicator) Stop() error {
	c.ls.Info(log_service.LogEvent{
		Message:  "Stopping HTTP communicator",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.httpServer.Shutdown(ctx)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to stop HTTP server",
			Metadata: map[string]any{"address": c.listenAddress, "error": err.Error()},
		})
		return ErrServerStopFailed
	}

	c.ls.Info(log_service.LogEvent{
		Message:  "HTTP communicator stopped successfully",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	return nil
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
	c.ls.Debug(log_service.LogEvent{
		Message:  "Sending HTTP message",
		Metadata: map[string]any{"to": to, "type": msg.Type, "from": msg.From},
	})

	c.clientLock.RLock()
	client, ok := c.clients[to]
	c.clientLock.RUnlock()

	if !ok {
		c.ls.Debug(log_service.LogEvent{
			Message:  "Creating new HTTP client",
			Metadata: map[string]any{"to": to},
		})

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
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to marshal message",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, ErrMessageMarshalFailed
	}

	url := fmt.Sprintf("http://%s/message", to)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to create HTTP request",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, ErrHTTPRequestCreateFailed
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to send HTTP request",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, ErrHTTPRequestSendFailed
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to read HTTP response body",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, ErrHTTPResponseReadFailed
	}

	c.ls.Debug(log_service.LogEvent{
		Message:  "HTTP message sent successfully",
		Metadata: map[string]any{"to": to, "type": msg.Type, "statusCode": resp.StatusCode},
	})

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
		c.ls.Warn(log_service.LogEvent{
			Message:  "HTTP method not allowed",
			Metadata: map[string]any{"method": r.Method, "remoteAddr": r.RemoteAddr},
		})
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to read HTTP request body",
			Metadata: map[string]any{"remoteAddr": r.RemoteAddr, "error": err.Error()},
		})
		http.Error(w, ErrHTTPBodyReadFailed.Error(), http.StatusBadRequest)
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
		c.ls.Error(log_service.LogEvent{
			Message:  "Invalid JSON in HTTP request",
			Metadata: map[string]any{"remoteAddr": r.RemoteAddr, "error": err.Error()},
		})
		http.Error(w, ErrInvalidJSON.Error(), http.StatusBadRequest)
		return
	}

	if rawMsg.From == "" || rawMsg.Type == "" {
		c.ls.Warn(log_service.LogEvent{
			Message:  "Missing required fields in HTTP request",
			Metadata: map[string]any{"remoteAddr": r.RemoteAddr, "from": rawMsg.From, "type": rawMsg.Type},
		})
		http.Error(w, ErrMissingRequiredFields.Error(), http.StatusBadRequest)
		return
	}

	c.ls.Debug(log_service.LogEvent{
		Message:  "Received HTTP message",
		Metadata: map[string]any{"from": rawMsg.From, "type": rawMsg.Type, "remoteAddr": r.RemoteAddr},
	})

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
				c.ls.Error(log_service.LogEvent{
					Message:  "Failed to unmarshal payload",
					Metadata: map[string]any{"from": rawMsg.From, "type": rawMsg.Type, "error": err.Error()},
				})
				http.Error(w, fmt.Sprintf("Invalid payload for message type %s: %v", rawMsg.Type, err), http.StatusBadRequest)
				return
			}

			// Dereference the pointer to get the actual value
			msg.Payload = reflect.ValueOf(payloadPtr).Elem().Interface()
			c.ls.Debug(log_service.LogEvent{
				Message:  "Payload deserialized successfully",
				Metadata: map[string]any{"from": rawMsg.From, "type": rawMsg.Type},
			})
		} else {
			// Empty payload - create zero value
			msg.Payload = reflect.Zero(payloadType).Interface()
		}
	} else {
		c.ls.Warn(log_service.LogEvent{
			Message:  "No payload type registered for message type",
			Metadata: map[string]any{"from": rawMsg.From, "type": rawMsg.Type},
		})
	}

	if c.handler == nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "HTTP handler not set",
			Metadata: map[string]any{"from": rawMsg.From, "type": rawMsg.Type},
		})
		http.Error(w, ErrHandlerNotSet.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := c.handler(msg)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Message handler error",
			Metadata: map[string]any{"from": rawMsg.From, "type": rawMsg.Type, "error": err.Error()},
		})
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

		c.ls.Debug(log_service.LogEvent{
			Message:  "HTTP response sent",
			Metadata: map[string]any{"to": msg.From, "code": resp.Code, "httpStatus": httpStatus},
		})
	} else {
		w.WriteHeader(http.StatusOK)
		c.ls.Debug(log_service.LogEvent{
			Message:  "HTTP response sent",
			Metadata: map[string]any{"to": msg.From, "code": "OK", "httpStatus": http.StatusOK},
		})
	}
}
