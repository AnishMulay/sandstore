package communication

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"reflect"
	"sync"

	communicationpb "github.com/AnishMulay/sandstore/gen/proto/communication"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCCommunicator struct {
	listenAddress string
	handler       MessageHandler
	grpcServer    *grpc.Server

	clientLock   sync.RWMutex
	clients      map[string]communicationpb.MessageServiceClient
	payloadTypes map[string]reflect.Type
}

func NewGRPCCommunicator(addr string) *GRPCCommunicator {
	c := &GRPCCommunicator{
		listenAddress: addr,
		clients:       make(map[string]communicationpb.MessageServiceClient),
		payloadTypes:  make(map[string]reflect.Type),
	}

	// Register default payload types
	c.payloadTypes[MessageTypeStoreFile] = reflect.TypeOf((*StoreFileRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeReadFile] = reflect.TypeOf((*ReadFileRequest)(nil)).Elem()
	c.payloadTypes[MessageTypeDeleteFile] = reflect.TypeOf((*DeleteFileRequest)(nil)).Elem()

	return c
}

func (c *GRPCCommunicator) Address() string {
	return c.listenAddress
}

func (c *GRPCCommunicator) Start(handler MessageHandler) error {
	c.handler = handler
	c.grpcServer = grpc.NewServer()
	communicationpb.RegisterMessageServiceServer(c.grpcServer, &grpcServer{comm: c})

	lis, err := net.Listen("tcp", c.listenAddress)
	if err != nil {
		return err
	}

	go func() {
		if err := c.grpcServer.Serve(lis); err != nil {
			log.Printf("GRPC server error: %v", err)
		}
	}()
	return nil
}

func (c *GRPCCommunicator) Stop() error {
	c.grpcServer.GracefulStop()
	return nil
}

func (c *GRPCCommunicator) Send(ctx context.Context, to string, msg Message) (*Response, error) {
	c.clientLock.RLock()
	client, ok := c.clients[to]
	c.clientLock.RUnlock()

	if !ok {
		conn, err := grpc.NewClient(to, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC client: %w", err)
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
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
	}

	req := &communicationpb.MessageRequest{
		From:    msg.From,
		Type:    msg.Type,
		Payload: payloadBytes,
	}

	log.Printf("Sending gRPC message type %s to %s", msg.Type, to)
	resp, err := client.SendMessage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}
	log.Printf("Received gRPC response with code: %s", resp.Code)

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
		return nil, fmt.Errorf("handler not set")
	}

	msg := Message{
		From: req.From,
		Type: req.Type,
	}

	log.Printf("Received gRPC message type %s from %s", req.Type, req.From)

	// Deserialize payload from JSON bytes
	if payloadType, exists := s.comm.payloadTypes[req.Type]; exists {
		if len(req.Payload) > 0 {
			// Create a new instance of the payload type
			payloadPtr := reflect.New(payloadType).Interface()

			if err := json.Unmarshal(req.Payload, payloadPtr); err != nil {
				return nil, fmt.Errorf("failed to unmarshal payload for type %s: %w", req.Type, err)
			}

			// Dereference the pointer to get the actual value
			msg.Payload = reflect.ValueOf(payloadPtr).Elem().Interface()
			log.Printf("Successfully deserialized payload for type %s", req.Type)
		} else {
			// Empty payload - create zero value
			msg.Payload = reflect.Zero(payloadType).Interface()
		}
	} else {
		log.Printf("No payload type registered for message type: %s", req.Type)
	}

	resp, err := s.comm.handler(msg)
	if err != nil {
		return nil, err
	}

	if resp == nil {
		log.Printf("Sent gRPC response to %s with code: OK", req.From)
		return &communicationpb.MessageResponse{Code: "OK"}, nil
	}

	log.Printf("Sent gRPC response to %s with code: %s", req.From, resp.Code)
	return &communicationpb.MessageResponse{
		Code:    string(resp.Code),
		Body:    resp.Body,
		Headers: resp.Headers,
	}, nil
}
