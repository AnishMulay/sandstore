package server

import (
	"context"
	"encoding/json"
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
	if handler, exists := s.handlers[msg.Type]; exists {
		return handler(msg)
	} else {
		return &communication.Response{
			Code: communication.CodeBadRequest,
			Body: []byte(fmt.Sprintf("No handler registered for message type: %s", msg.Type)),
		}, nil
	}
}

func (s *DefaultServer) HandleStoreFileMessage(msg communication.Message) (*communication.Response, error) {
	var request communication.StoreFileRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return &communication.Response{
			Code: communication.CodeBadRequest,
			Body: []byte("Invalid store file request"),
		}, nil
	}

	err := s.fs.StoreFile(request.Path, request.Data)
	if err != nil {
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to store file: %v", err)),
		}, nil
	}

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *DefaultServer) HandleReadFileMessage(msg communication.Message) (*communication.Response, error) {
	var request communication.ReadFileRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return &communication.Response{
			Code: communication.CodeBadRequest,
			Body: []byte("Invalid read file request"),
		}, nil
	}

	data, err := s.fs.ReadFile(request.Path)
	if err != nil {
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to read file: %v", err)),
		}, nil
	}

	return &communication.Response{
		Code: communication.CodeOK,
		Body: data,
	}, nil
}

func (s *DefaultServer) HandleDeleteFileMessage(msg communication.Message) (*communication.Response, error) {
	var request communication.DeleteFileRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		return &communication.Response{
			Code: communication.CodeBadRequest,
			Body: []byte("Invalid delete file request"),
		}, nil
	}

	err := s.fs.DeleteFile(request.Path)
	if err != nil {
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to delete file: %v", err)),
		}, nil
	}

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}
