package server

import (
	"context"
	"sync"

	"github.com/AnishMulay/sandstore/internal/communication"
)

type Server struct {
	communicator communication.Communicator
	handlers     map[string]communication.MessageHandler
	handlersLock sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
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
