package server

import (
	"context"
	"fmt"
	"reflect"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
)

type DefaultServer struct {
	comm         communication.Communicator
	fs           file_service.FileService
	ctx          context.Context
	cancel       context.CancelFunc
	typedHandlers map[string]*TypedHandler
}

func NewDefaultServer(comm communication.Communicator, fs file_service.FileService) *DefaultServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &DefaultServer{
		comm:         comm,
		fs:           fs,
		ctx:          ctx,
		cancel:       cancel,
		typedHandlers: make(map[string]*TypedHandler),
	}
}

func (s *DefaultServer) Start() error {
	return s.comm.Start(s.handleMessage)
}

func (s *DefaultServer) Stop() error {
	s.cancel()
	return s.comm.Stop()
}



func (s *DefaultServer) RegisterTypedHandler(msgType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	s.typedHandlers[msgType] = &TypedHandler{
		Handler:     handler,
		PayloadType: payloadType,
	}
}

func (s *DefaultServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	if typedHandler, exists := s.typedHandlers[msg.Type]; exists {
		// Type check the payload
		if msg.Payload != nil {
			actualType := reflect.TypeOf(msg.Payload)
			if actualType != typedHandler.PayloadType {
				return &communication.Response{
					Code: communication.CodeBadRequest,
					Body: []byte(fmt.Sprintf("Invalid payload type for %s: expected %s, got %s", msg.Type, typedHandler.PayloadType, actualType)),
				}, nil
			}
		}
		return typedHandler.Handler(msg)
	}
	
	return &communication.Response{
		Code: communication.CodeBadRequest,
		Body: []byte(fmt.Sprintf("No handler registered for message type: %s", msg.Type)),
	}, nil
}

func (s *DefaultServer) HandleStoreFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreFileRequest)

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
	request := msg.Payload.(communication.ReadFileRequest)

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
	request := msg.Payload.(communication.DeleteFileRequest)

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
