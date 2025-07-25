package server

import (
	"context"
	"reflect"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/node_registry"
)

type DefaultServer struct {
	comm          communication.Communicator
	fs            file_service.FileService
	ls            log_service.LogService
	ctx           context.Context
	cancel        context.CancelFunc
	typedHandlers map[string]*TypedHandler
	nodeRegistry  node_registry.NodeRegistry
}

func NewDefaultServer(comm communication.Communicator, fs file_service.FileService, ls log_service.LogService, nr node_registry.NodeRegistry) *DefaultServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &DefaultServer{
		comm:          comm,
		fs:            fs,
		ls:            ls,
		ctx:           ctx,
		cancel:        cancel,
		typedHandlers: make(map[string]*TypedHandler),
		nodeRegistry:  nr,
	}
}

func (s *DefaultServer) Start() error {
	s.ls.Info(log_service.LogEvent{
		Message: "Starting default server",
	})

	err := s.comm.Start(s.handleMessage)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to start server",
			Metadata: map[string]any{"error": err.Error()},
		})
		return ErrServerStartFailed
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Default server started successfully",
	})
	return nil
}

func (s *DefaultServer) Stop() error {
	s.ls.Info(log_service.LogEvent{
		Message: "Stopping default server",
	})

	s.cancel()
	err := s.comm.Stop()
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to stop server",
			Metadata: map[string]any{"error": err.Error()},
		})
		return ErrServerStopFailed
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Default server stopped successfully",
	})
	return nil
}

func (s *DefaultServer) RegisterTypedHandler(msgType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	s.ls.Debug(log_service.LogEvent{
		Message:  "Registering typed handler",
		Metadata: map[string]any{"messageType": msgType, "payloadType": payloadType.String()},
	})

	s.typedHandlers[msgType] = &TypedHandler{
		Handler:     handler,
		PayloadType: payloadType,
	}
}

func (s *DefaultServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	s.ls.Debug(log_service.LogEvent{
		Message:  "Processing message",
		Metadata: map[string]any{"type": msg.Type},
	})

	if typedHandler, exists := s.typedHandlers[msg.Type]; exists {
		// Type check the payload
		if msg.Payload != nil {
			actualType := reflect.TypeOf(msg.Payload)
			if actualType != typedHandler.PayloadType {
				s.ls.Warn(log_service.LogEvent{
					Message: "Invalid payload type",
					Metadata: map[string]any{
						"messageType": msg.Type,
						"expected":    typedHandler.PayloadType.String(),
						"actual":      actualType.String(),
					},
				})
				return &communication.Response{
					Code: communication.CodeBadRequest,
					Body: []byte(ErrInvalidPayloadType.Error()),
				}, nil
			}
		}
		return typedHandler.Handler(msg)
	}

	s.ls.Warn(log_service.LogEvent{
		Message:  "No handler registered for message type",
		Metadata: map[string]any{"type": msg.Type},
	})

	return &communication.Response{
		Code: communication.CodeBadRequest,
		Body: []byte(ErrHandlerNotRegistered.Error()),
	}, nil
}

func (s *DefaultServer) HandleStoreFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreFileRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Storing file",
		Metadata: map[string]any{"path": request.Path, "size": len(request.Data)},
	})

	err := s.fs.StoreFile(request.Path, request.Data)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to store file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(ErrFileStoreFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "File stored successfully",
		Metadata: map[string]any{"path": request.Path},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *DefaultServer) HandleReadFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.ReadFileRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Reading file",
		Metadata: map[string]any{"path": request.Path},
	})

	data, err := s.fs.ReadFile(request.Path)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to read file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(ErrFileReadFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "File read successfully",
		Metadata: map[string]any{"path": request.Path, "size": len(data)},
	})

	return &communication.Response{
		Code: communication.CodeOK,
		Body: data,
	}, nil
}

func (s *DefaultServer) HandleDeleteFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.DeleteFileRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Deleting file",
		Metadata: map[string]any{"path": request.Path},
	})

	err := s.fs.DeleteFile(request.Path)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to delete file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(ErrFileDeleteFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "File deleted successfully",
		Metadata: map[string]any{"path": request.Path},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}
