package grpccomm

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"sync"

	communicationpb "github.com/AnishMulay/sandstore/gen/proto/communication"
	"github.com/AnishMulay/sandstore/internal/communication"
	internalerrors "github.com/AnishMulay/sandstore/internal/communication/internal"
	"github.com/AnishMulay/sandstore/internal/log_service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCCommunicator struct {
	listenAddress string
	handler       communication.MessageHandler
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

func (c *GRPCCommunicator) Address() string {
	return c.listenAddress
}

func (c *GRPCCommunicator) Start(handler communication.MessageHandler) error {
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
		return internalerrors.ErrGRPCListenFailed
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

func (c *GRPCCommunicator) Send(ctx context.Context, to string, msg communication.Message) (*communication.Response, error) {
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
			return nil, internalerrors.ErrClientCreateFailed
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
			return nil, internalerrors.ErrPayloadMarshalFailed
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
		return nil, internalerrors.ErrMessageSendFailed
	}

	c.ls.Debug(log_service.LogEvent{
		Message:  "GRPC message sent successfully",
		Metadata: map[string]any{"to": to, "type": msg.Type, "responseCode": resp.Code},
	})

	return &communication.Response{
		Code:    communication.SandCode(resp.Code),
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
		return nil, internalerrors.ErrHandlerNotSet
	}

	msg := communication.Message{
		From: req.From,
		Type: req.Type,
	}

	// Deserialize payload based on registered type
	if req.Payload != nil {
		payloadType, ok := s.comm.payloadTypes[req.Type]
		if !ok {
			return nil, internalerrors.ErrPayloadUnmarshalFailed
		}

		payload := reflect.New(payloadType).Interface()
		if err := json.Unmarshal(req.Payload, payload); err != nil {
			return nil, internalerrors.ErrPayloadUnmarshalFailed
		}

		msg.Payload = reflect.ValueOf(payload).Elem().Interface()
	}

	resp, err := s.comm.handler(msg)
	if err != nil {
		s.comm.ls.Error(log_service.LogEvent{
			Message:  "Message handler failed",
			Metadata: map[string]any{"type": req.Type, "error": err.Error()},
		})

		return &communicationpb.MessageResponse{
			Code: string(communication.CodeInternal),
			Body: []byte(err.Error()),
		}, nil
	}

	if resp == nil {
		return &communicationpb.MessageResponse{
			Code: string(communication.CodeInternal),
			Body: []byte("handler returned nil response"),
		}, nil
	}

	return &communicationpb.MessageResponse{
		Code:    string(resp.Code),
		Body:    resp.Body,
		Headers: resp.Headers,
	}, nil
}
