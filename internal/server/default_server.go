package server

import (
	"context"
	"fmt"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
)

type DefaultServer struct {
	comm     communication.Communicator
	fs       file_service.FileService
	ctx      context.Context
	cancel   context.CancelFunc
	handlers map[string]func(msg communication.Message) (*communication.Response, error)
}

func NewDefaultServer(comm communication.Communicator, fs file_service.FileService) *DefaultServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &DefaultServer{
		comm:   comm,
		fs:     fs,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *DefaultServer) Start() error {
	return s.comm.Start(s.handleMessage)
}

func (s *DefaultServer) Stop() error {
	s.cancel()
	return s.comm.Stop()
}

func (s *DefaultServer) RegisterHandler(msgType string, handler func(msg communication.Message) (*communication.Response, error)) {
	if s.handlers == nil {
		s.handlers = make(map[string]func(msg communication.Message) (*communication.Response, error))
	}
	s.handlers[msgType] = handler
}

func (s *DefaultServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	fmt.Printf("Received message: %v\n", msg)
	return &communication.Response{
		Code: communication.CodeOK,
		Body: []byte("OK"),
	}, nil
}
