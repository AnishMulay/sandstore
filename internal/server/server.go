package server

import (
	"context"
	"log"
	"sync"
	"time"

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

func (s *Server) messageLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			msg, err := s.communicator.Receive(s.ctx)
			if err != nil {
				log.Printf("Error receiving message: %v", err)
				continue
			}
			go s.handleMessage(msg)
		}
	}
}

func (s *Server) Start() error {
	if err := s.communicator.Start(); err != nil {
		return err
	}
	s.wg.Add(1)
	go s.messageLoop()
	log.Printf("Server started, listening on %s", s.communicator.Address())
	return nil
}

func (s *Server) handleMessage(msg communication.Message) {
	s.handlersLock.RLock()
	handler, exists := s.handlers[msg.Type]
	s.handlersLock.RUnlock()

	if !exists {
		log.Printf("No handler registered for message type: %s", msg.Type)
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, defaultHandlerTimeout)
	defer cancel()

	response, err := handler(ctx, msg)
	if err != nil {
		log.Printf("Error handling message: %v", err)
		return
	}

	if response != nil {
		if err := s.communicator.Send(ctx, msg.From, "response", response); err != nil {
			log.Printf("Error sending response: %v", err)
		}
	}
}

const defaultHandlerTimeout = 30 * time.Second
