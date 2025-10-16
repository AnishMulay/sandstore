package replicatedserver

import (
	"context"
	"reflect"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/server"
	internalerrors "github.com/AnishMulay/sandstore/internal/server/internal"
)

type ReplicatedServer struct {
	comm           communication.Communicator
	fs             file_service.FileService
	cs             chunk_service.ChunkService
	ms             metadata_service.MetadataService
	ls             log_service.LogService
	ctx            context.Context
	cancel         context.CancelFunc
	typedHandlers  map[string]*server.TypedHandler
	clusterService cluster_service.ClusterService
}

func NewReplicatedServer(comm communication.Communicator, fs file_service.FileService, cs chunk_service.ChunkService, ms metadata_service.MetadataService, ls log_service.LogService, clusterService cluster_service.ClusterService) *ReplicatedServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &ReplicatedServer{
		comm:           comm,
		fs:             fs,
		cs:             cs,
		ms:             ms,
		ls:             ls,
		ctx:            ctx,
		cancel:         cancel,
		typedHandlers:  make(map[string]*server.TypedHandler),
		clusterService: clusterService,
	}
}

func (s *ReplicatedServer) Start() error {
	s.ls.Info(log_service.LogEvent{Message: "Starting replicated server"})

	if err := s.comm.Start(s.handleMessage); err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to start server",
			Metadata: map[string]any{"error": err.Error()},
		})
		return internalerrors.ErrServerStartFailed
	}

	s.ls.Info(log_service.LogEvent{Message: "Replicated server started successfully"})
	return nil
}

func (s *ReplicatedServer) Stop() error {
	s.ls.Info(log_service.LogEvent{Message: "Stopping replicated server"})

	s.cancel()
	if err := s.comm.Stop(); err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to stop server",
			Metadata: map[string]any{"error": err.Error()},
		})
		return internalerrors.ErrServerStopFailed
	}

	s.ls.Info(log_service.LogEvent{Message: "Replicated server stopped successfully"})
	return nil
}

func (s *ReplicatedServer) RegisterTypedHandler(msgType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	s.ls.Debug(log_service.LogEvent{
		Message:  "Registering typed handler",
		Metadata: map[string]any{"messageType": msgType, "payloadType": payloadType.String()},
	})

	s.typedHandlers[msgType] = &server.TypedHandler{
		Handler:     handler,
		PayloadType: payloadType,
	}
}

func (s *ReplicatedServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	s.ls.Debug(log_service.LogEvent{
		Message:  "Processing message",
		Metadata: map[string]any{"type": msg.Type},
	})

	if typedHandler, exists := s.typedHandlers[msg.Type]; exists {
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
					Body: []byte(internalerrors.ErrInvalidPayloadType.Error()),
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
		Body: []byte(internalerrors.ErrHandlerNotRegistered.Error()),
	}, nil
}

func (s *ReplicatedServer) HandleStoreFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreFileRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Storing file",
		Metadata: map[string]any{"path": request.Path, "size": len(request.Data)},
	})

	if err := s.fs.StoreFile(request.Path, request.Data); err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to store file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(internalerrors.ErrFileStoreFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "File stored successfully",
		Metadata: map[string]any{"path": request.Path},
	})

	return &communication.Response{Code: communication.CodeOK}, nil
}

func (s *ReplicatedServer) HandleReadFileMessage(msg communication.Message) (*communication.Response, error) {
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
			Body: []byte(internalerrors.ErrFileReadFailed.Error()),
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

func (s *ReplicatedServer) HandleDeleteFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.DeleteFileRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Deleting file",
		Metadata: map[string]any{"path": request.Path},
	})

	if err := s.fs.DeleteFile(request.Path); err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to delete file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(internalerrors.ErrFileDeleteFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "File deleted successfully",
		Metadata: map[string]any{"path": request.Path},
	})

	return &communication.Response{Code: communication.CodeOK}, nil
}

var _ server.Server = (*ReplicatedServer)(nil)
