package server

import (
	"context"
	"encoding/json"
	"log"

	"github.com/AnishMulay/sandstore/internal/communication"
)

type Server struct {
	communicator communication.Communicator
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewServer(communicator communication.Communicator) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		communicator: communicator,
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (s *Server) handleMessage(msg communication.Message) (*communication.Response, error) {
	log.Printf("Received message of type: %s from: %s", msg.Type, msg.From)

	switch msg.Type {
	case "ping":
		log.Printf("Responding to ping from %s", msg.From)
		// Create a Message to maintain backward compatibility with client
		responseMsg := &communication.Message{
			Type:    "pong",
			Payload: []byte("pong"),
		}
		
		// Convert to JSON for the response body
		jsonData, err := json.Marshal(responseMsg)
		if err != nil {
			return &communication.Response{
				Code: communication.CodeInternal,
				Body: []byte("Error encoding response"),
			}, nil
		}
		
		return &communication.Response{
			Code: communication.CodeOK,
			Body: jsonData,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		}, nil
	default:
		log.Printf("Unhandled message type: %s", msg.Type)
		return &communication.Response{
			Code: communication.CodeOK,
			Body: nil,
		}, nil
	}
}

func (s *Server) Start() error {
	if err := s.communicator.Start(s.handleMessage); err != nil {
		return err
	}
	log.Printf("Server started, listening on %s", s.communicator.Address())
	return nil
}

func (s *Server) Stop() error {
	s.cancel()
	if err := s.communicator.Stop(); err != nil {
		return err
	}
	log.Printf("Server stopped")
	return nil
}

func (s *Server) Send(ctx context.Context, to string, msgType string, payload []byte) error {
	msg := communication.Message{
		Type:    msgType,
		Payload: payload,
	}
	return s.communicator.Send(ctx, to, msg)
}