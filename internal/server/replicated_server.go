package server

import (
	"context"
	"fmt"
	"reflect"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/node_registry"
)

type ReplicatedServer struct {
	comm          communication.Communicator
	fs            file_service.FileService
	cs            chunk_service.ChunkService
	ctx           context.Context
	cancel        context.CancelFunc
	typedHandlers map[string]*TypedHandler
	nodeRegistry  node_registry.NodeRegistry
}

func NewReplicatedServer(comm communication.Communicator, fs file_service.FileService, cs chunk_service.ChunkService, nr node_registry.NodeRegistry) *ReplicatedServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &ReplicatedServer{
		comm:          comm,
		fs:            fs,
		cs:            cs,
		ctx:           ctx,
		cancel:        cancel,
		typedHandlers: make(map[string]*TypedHandler),
		nodeRegistry:  nr,
	}
}

func (s *ReplicatedServer) Start() error {
	return s.comm.Start(s.handleMessage)
}

func (s *ReplicatedServer) Stop() error {
	s.cancel()
	return s.comm.Stop()
}

func (s *ReplicatedServer) RegisterTypedHandler(msgType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	s.typedHandlers[msgType] = &TypedHandler{
		Handler:     handler,
		PayloadType: payloadType,
	}
}

func (s *ReplicatedServer) handleMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *ReplicatedServer) HandleStoreFileMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *ReplicatedServer) HandleReadFileMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *ReplicatedServer) HandleDeleteFileMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *ReplicatedServer) HandleStoreChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreChunkRequest)

	err := s.cs.WriteChunk(request.ChunkID, request.Data)
	if err != nil {
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to store chunk: %v", err)),
		}, nil
	}

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *ReplicatedServer) HandleReadChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.ReadChunkRequest)

	data, err := s.cs.ReadChunk(request.ChunkID)
	if err != nil {
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to read chunk: %v", err)),
		}, nil
	}

	return &communication.Response{
		Code: communication.CodeOK,
		Body: data,
	}, nil
}
