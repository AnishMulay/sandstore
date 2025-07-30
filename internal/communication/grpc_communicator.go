package communication

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"sync"

	communicationpb "github.com/AnishMulay/sandstore/gen/proto/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCCommunicator struct {
	listenAddress string
	handler       MessageHandler
	grpcServer    *grpc.Server
	ls            log_service.LogService

	clientLock   sync.RWMutex
	clients      map[string]communicationpb.MessageServiceClient
	payloadTypes map[string]reflect.Type
	stopped      bool
	stopMutex    sync.RWMutex
}

func NewGRPCCommunicator(addr string, ls log_service.LogService) *GRPCCommunicator {
	c := &GRPCCommunicator{
		listenAddress: addr,
		ls:            ls,
		clients:       make(map[string]communicationpb.MessageServiceClient),
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
	c.payloadTypes[MessageTypeStopServer] = reflect.TypeOf((*StopServerRequest)(nil)).Elem()

	return c
}

func (c *GRPCCommunicator) Address() string {
	return c.listenAddress
}

func (c *GRPCCommunicator) Start(handler MessageHandler) error {
	c.ls.Info(log_service.LogEvent{
		Message:  "Starting GRPC communicator",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	c.handler = handler
	c.grpcServer = grpc.NewServer()
	communicationpb.RegisterMessageServiceServer(c.grpcServer, &grpcServer{comm: c})

	lis, err := net.Listen("tcp", c.listenAddress)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to listen on address",
			Metadata: map[string]any{"address": c.listenAddress, "error": err.Error()},
		})
		return ErrGRPCListenFailed
	}

	c.ls.Info(log_service.LogEvent{
		Message:  "GRPC communicator started successfully",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	go func() {
		if err := c.grpcServer.Serve(lis); err != nil {
			c.ls.Error(log_service.LogEvent{
				Message:  "GRPC server error",
				Metadata: map[string]any{"address": c.listenAddress, "error": err.Error()},
			})
		}
	}()
	return nil
}

func (c *GRPCCommunicator) Stop() error {
	c.stopMutex.Lock()
	defer c.stopMutex.Unlock()

	if c.stopped {
		c.ls.Debug(log_service.LogEvent{
			Message:  "GRPC communicator already stopped, skipping",
			Metadata: map[string]any{"address": c.listenAddress},
		})
		return nil
	}

	c.ls.Info(log_service.LogEvent{
		Message:  "Stopping GRPC communicator",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	if c.grpcServer != nil {
		c.grpcServer.GracefulStop()
	}

	c.stopped = true
	c.ls.Info(log_service.LogEvent{
		Message:  "GRPC communicator stopped successfully",
		Metadata: map[string]any{"address": c.listenAddress},
	})

	return nil
}

func (c *GRPCCommunicator) Send(ctx context.Context, to string, msg Message) (*Response, error) {
	c.ls.Debug(log_service.LogEvent{
		Message:  "Sending GRPC message",
		Metadata: map[string]any{"to": to, "type": msg.Type, "from": msg.From},
	})

	c.clientLock.RLock()
	client, ok := c.clients[to]
	c.clientLock.RUnlock()

	if !ok {
		c.ls.Debug(log_service.LogEvent{
			Message:  "Creating new GRPC client",
			Metadata: map[string]any{"to": to},
		})

		conn, err := grpc.NewClient(to, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			c.ls.Error(log_service.LogEvent{
				Message:  "Failed to create GRPC client",
				Metadata: map[string]any{"to": to, "error": err.Error()},
			})
			return nil, ErrClientCreateFailed
		}
		client = communicationpb.NewMessageServiceClient(conn)
		c.clientLock.Lock()
		c.clients[to] = client
		c.clientLock.Unlock()
	}

	// Serialize payload to JSON bytes
	var payloadBytes []byte
	if msg.Payload != nil {
		var err error
		payloadBytes, err = json.Marshal(msg.Payload)
		if err != nil {
			c.ls.Error(log_service.LogEvent{
				Message:  "Failed to marshal payload",
				Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
			})
			return nil, ErrPayloadMarshalFailed
		}
	}

	req := &communicationpb.MessageRequest{
		From:    msg.From,
		Type:    msg.Type,
		Payload: payloadBytes,
	}

	resp, err := client.SendMessage(ctx, req)
	if err != nil {
		c.ls.Error(log_service.LogEvent{
			Message:  "Failed to send GRPC message",
			Metadata: map[string]any{"to": to, "type": msg.Type, "error": err.Error()},
		})
		return nil, ErrMessageSendFailed
	}

	c.ls.Debug(log_service.LogEvent{
		Message:  "GRPC message sent successfully",
		Metadata: map[string]any{"to": to, "type": msg.Type, "responseCode": resp.Code},
	})

	return &Response{
		Code:    SandCode(resp.Code),
		Body:    resp.Body,
		Headers: resp.Headers,
	}, nil
}

type grpcServer struct {
	communicationpb.UnimplementedMessageServiceServer
	comm *GRPCCommunicator
}

func (s *grpcServer) SendMessage(ctx context.Context, req *communicationpb.MessageRequest) (*communicationpb.MessageResponse, error) {
	if s.comm.handler == nil {
		s.comm.ls.Error(log_service.LogEvent{
			Message:  "GRPC handler not set",
			Metadata: map[string]any{"from": req.From, "type": req.Type},
		})
		return nil, ErrHandlerNotSet
	}

	s.comm.ls.Debug(log_service.LogEvent{
		Message:  "Received GRPC message",
		Metadata: map[string]any{"from": req.From, "type": req.Type},
	})

	msg := Message{
		From: req.From,
		Type: req.Type,
	}

	// Deserialize payload from JSON bytes
	if payloadType, exists := s.comm.payloadTypes[req.Type]; exists {
		if len(req.Payload) > 0 {
			// Create a new instance of the payload type
			payloadPtr := reflect.New(payloadType).Interface()

			if err := json.Unmarshal(req.Payload, payloadPtr); err != nil {
				s.comm.ls.Error(log_service.LogEvent{
					Message:  "Failed to unmarshal payload",
					Metadata: map[string]any{"from": req.From, "type": req.Type, "error": err.Error()},
				})
				return nil, ErrPayloadUnmarshalFailed
			}

			// Dereference the pointer to get the actual value
			msg.Payload = reflect.ValueOf(payloadPtr).Elem().Interface()
			s.comm.ls.Debug(log_service.LogEvent{
				Message:  "Payload deserialized successfully",
				Metadata: map[string]any{"from": req.From, "type": req.Type},
			})
		} else {
			// Empty payload - create zero value
			msg.Payload = reflect.Zero(payloadType).Interface()
		}
	} else {
		s.comm.ls.Warn(log_service.LogEvent{
			Message:  "No payload type registered for message type",
			Metadata: map[string]any{"from": req.From, "type": req.Type},
		})
	}

	resp, err := s.comm.handler(msg)
	if err != nil {
		s.comm.ls.Error(log_service.LogEvent{
			Message:  "Message handler error",
			Metadata: map[string]any{"from": req.From, "type": req.Type, "error": err.Error()},
		})
		return nil, err
	}

	if resp == nil {
		s.comm.ls.Debug(log_service.LogEvent{
			Message:  "GRPC response sent",
			Metadata: map[string]any{"to": req.From, "code": "OK"},
		})
		return &communicationpb.MessageResponse{Code: "OK"}, nil
	}

	s.comm.ls.Debug(log_service.LogEvent{
		Message:  "GRPC response sent",
		Metadata: map[string]any{"to": req.From, "code": resp.Code},
	})

	return &communicationpb.MessageResponse{
		Code:    string(resp.Code),
		Body:    resp.Body,
		Headers: resp.Headers,
	}, nil
}
