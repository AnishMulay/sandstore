package httpcomm

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

	"github.com/AnishMulay/sandstore/internal/communication"
	internalerrors "github.com/AnishMulay/sandstore/internal/communication/internal"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type HTTPCommunicator struct {
	listenAddress string
	httpServer    *http.Server
	handler       communication.MessageHandler
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
	c.payloadTypes[communication.MessageTypeStoreFile] = reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeReadFile] = reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeDeleteFile] = reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeStoreChunk] = reflect.TypeOf((*communication.StoreChunkRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeReadChunk] = reflect.TypeOf((*communication.ReadChunkRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeDeleteChunk] = reflect.TypeOf((*communication.DeleteChunkRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeStoreMetadata] = reflect.TypeOf((*communication.StoreMetadataRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeDeleteMetadata] = reflect.TypeOf((*communication.DeleteMetadataRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeStopServer] = reflect.TypeOf((*communication.StopServerRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeRequestVote] = reflect.TypeOf((*communication.RequestVoteRequest)(nil)).Elem()
	c.payloadTypes[communication.MessageTypeAppendEntries] = reflect.TypeOf((*communication.AppendEntriesRequest)(nil)).Elem()

	return c
}

func (c *HTTPCommunicator) Address() string {
	return c.listenAddress
}

func (c *HTTPCommunicator) Start(handler communication.MessageHandler) error {
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
		return internalerrors.ErrServerStopFailed
	}

	c.ls.Info(log_service.LogEvent{
		Message:  "HTTP communicator stopped successfully",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	return nil
}

func mapFromHTTPCode(code int) communication.SandCode {
	switch code {
	case http.StatusOK:
		return communication.CodeOK
	case http.StatusBadRequest:
		return communication.CodeBadRequest
	case http.StatusNotFound:
		return communication.CodeNotFound
	case http.StatusInternalServerError:
		return communication.CodeInternal
	case http.StatusServiceUnavailable:
		return communication.CodeUnavailable
	default:
		return communication.CodeInternal // Default to internal error for unknown codes
	}
}

func (c *HTTPCommunicator) Send(ctx context.Context, to string, msg communication.Message) (*communication.Response, error) {
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
		return nil, internalerrors.ErrMessageMarshalFailed
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/message", to), bytes.NewBuffer(jsonData))
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to create HTTP request",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, internalerrors.ErrHTTPRequestCreateFailed
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to send HTTP request",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, internalerrors.ErrHTTPRequestSendFailed
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to read HTTP response",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, internalerrors.ErrHTTPResponseReadFailed
	}

	c.ls.Debug(log_service.LogEvent{
		Message:  "HTTP message sent successfully",
		Metadata: map[string]any{"to": to, "type": msg.Type, "status": resp.StatusCode},
	})

	return &communication.Response{
		Code: mapFromHTTPCode(resp.StatusCode),
		Body: body,
	}, nil
}

func (c *HTTPCommunicator) handleHTTPMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to read HTTP request body",
			Metadata: map[string]any{"error": err.Error()},
		})
		http.Error(w, internalerrors.ErrHTTPBodyReadFailed.Error(), http.StatusBadRequest)
		return
	}

	var msg communication.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Invalid JSON in request",
			Metadata: map[string]any{"error": err.Error()},
		})
		http.Error(w, internalerrors.ErrInvalidJSON.Error(), http.StatusBadRequest)
		return
	}

	if msg.Type == "" {
		http.Error(w, internalerrors.ErrMissingRequiredFields.Error(), http.StatusBadRequest)
		return
	}

	if c.handler == nil {
		http.Error(w, internalerrors.ErrHandlerNotSet.Error(), http.StatusServiceUnavailable)
		return
	}

	payloadType, ok := c.payloadTypes[msg.Type]
	if ok && msg.Payload != nil {
		var payloadValue reflect.Value
		if rawMap, isMap := msg.Payload.(map[string]any); isMap {
			payloadValue = reflect.New(payloadType)
			bytes, err := json.Marshal(rawMap)
			if err != nil {
				c.ls.Error(log_service.LogEvent{
					Message:  "Failed to marshal payload map",
					Metadata: map[string]any{"error": err.Error()},
				})
				http.Error(w, internalerrors.ErrPayloadMarshalFailed.Error(), http.StatusBadRequest)
				return
			}

			if err := json.Unmarshal(bytes, payloadValue.Interface()); err != nil {
				c.ls.Error(log_service.LogEvent{
					Message:  "Failed to unmarshal payload into struct",
					Metadata: map[string]any{"error": err.Error()},
				})
				http.Error(w, internalerrors.ErrPayloadUnmarshalFailed.Error(), http.StatusBadRequest)
				return
			}
		} else {
			payloadValue = reflect.New(payloadType)
			bytes, err := json.Marshal(msg.Payload)
			if err != nil {
				http.Error(w, internalerrors.ErrPayloadMarshalFailed.Error(), http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(bytes, payloadValue.Interface()); err != nil {
				http.Error(w, internalerrors.ErrPayloadUnmarshalFailed.Error(), http.StatusBadRequest)
				return
			}
		}

		msg.Payload = payloadValue.Elem().Interface()
	}

	resp, err := c.handler(msg)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Message handler failed",
			Metadata: map[string]any{"type": msg.Type, "error": err.Error()},
		})
		http.Error(w, internalerrors.ErrMessageHandlerFailed.Error(), http.StatusInternalServerError)
		return
	}

	if resp == nil {
		http.Error(w, internalerrors.ErrMessageHandlerFailed.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if resp.Body != nil {
		if _, err := w.Write(resp.Body); err != nil {
			c.ls.Error(log_service.LogEvent{
				Message:  "Failed to write HTTP response body",
				Metadata: map[string]any{"error": err.Error()},
			})
		}
	}
}
