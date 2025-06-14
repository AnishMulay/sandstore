package server

import (
	"context"
	"log"
	"sync"

	"github.com/AnishMulay/sandstore/internal/communication"
)

type Server struct {
	communicator communication.Communicator
	handlers     map[string]communication.MessageHandler
	handlersLock sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewServer(communicator communication.Communicator) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		communicator: communicator,
		handlers:     make(map[string]communication.MessageHandler),
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (s *Server) RegisterHandler(messageType string, handler communication.MessageHandler) {
	s.handlersLock.Lock()
	defer s.handlersLock.Unlock()
	s.handlers[messageType] = handler
}

func (s *Server) handleMessage(msg communication.Message) (*communication.Message, error) {
	s.handlersLock.RLock()
	handler, exists := s.handlers[msg.Type]
	s.handlersLock.RUnlock()

	if !exists {
		log.Printf("No handler registered for message type: %s", msg.Type)
		return nil, nil
	}

	return handler(msg)
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