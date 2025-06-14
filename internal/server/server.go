package server

import (
	"context"
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

func (s *Server) handleMessage(msg communication.Message) (*communication.Message, error) {
	log.Printf("Received message of type: %s from: %s", msg.Type, msg.From)

	switch msg.Type {
	case "ping":
		log.Printf("Responding to ping from %s", msg.From)
		return &communication.Message{
			Type:    "pong",
			Payload: []byte("pong"),
		}, nil
	default:
		log.Printf("Unhandled message type: %s", msg.Type)
		return nil, nil
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