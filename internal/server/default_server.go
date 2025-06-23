package server

import (
	"context"
	"fmt"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
)

type DefaultServer struct {
	comm   communication.Communicator
	fs     file_service.FileService
	ctx    context.Context
	cancel context.CancelFunc
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

func (s *DefaultServer) Start() {
	s.comm.Start(s.handleMessage)
}

func (s *DefaultServer) Stop() {
	s.comm.Stop()
	s.cancel()
}

func (s *DefaultServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	fmt.Printf("Received message: %v\n", msg)
	return &communication.Response{
		Code: communication.CodeOK,
		Body: []byte("OK"),
	}, nil
}
