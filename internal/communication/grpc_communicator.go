package communication

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	communicationpb "github.com/AnishMulay/sandstore/gen/proto/communication"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCCommunicator struct {
	listenAddress string
	handler       MessageHandler
	grpcServer    *grpc.Server

	clientLock sync.RWMutex
	clients    map[string]communicationpb.MessageServiceClient
}

func NewGRPCCommunicator(addr string) *GRPCCommunicator {
	return &GRPCCommunicator{
		listenAddress: addr,
		clients:       make(map[string]communicationpb.MessageServiceClient),
	}
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

	req := &communicationpb.MessageRequest{
		From:    msg.From,
		Type:    msg.Type,
		Payload: msg.Payload,
	}

	resp, err := client.SendMessage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

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
		From:    req.From,
		Type:    req.Type,
		Payload: req.Payload,
	}
	resp, err := s.comm.handler(msg)
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return &communicationpb.MessageResponse{Code: "OK"}, nil
	}

	return &communicationpb.MessageResponse{
		Code:    string(resp.Code),
		Body:    resp.Body,
		Headers: resp.Headers,
	}, nil
}
