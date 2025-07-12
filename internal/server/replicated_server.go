package server

import (
	"context"
	"fmt"
	"reflect"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/node_registry"
)

type ReplicatedServer struct {
	comm          communication.Communicator
	fs            file_service.FileService
	cs            chunk_service.ChunkService
	ms            metadata_service.MetadataService
	ls            log_service.LogService
	ctx           context.Context
	cancel        context.CancelFunc
	typedHandlers map[string]*TypedHandler
	nodeRegistry  node_registry.NodeRegistry
}

func NewReplicatedServer(comm communication.Communicator, fs file_service.FileService, cs chunk_service.ChunkService, ms metadata_service.MetadataService, ls log_service.LogService, nr node_registry.NodeRegistry) *ReplicatedServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &ReplicatedServer{
		comm:          comm,
		fs:            fs,
		cs:            cs,
		ms:            ms,
		ls:            ls,
		ctx:           ctx,
		cancel:        cancel,
		typedHandlers: make(map[string]*TypedHandler),
		nodeRegistry:  nr,
	}
}

func (s *ReplicatedServer) Start() error {
	s.ls.Info(log_service.LogEvent{
		Message: "Starting replicated server",
	})
	
	err := s.comm.Start(s.handleMessage)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to start server",
			Metadata: map[string]any{"error": err.Error()},
		})
		return err
	}
	
	s.ls.Info(log_service.LogEvent{
		Message: "Replicated server started successfully",
	})
	return nil
}

func (s *ReplicatedServer) Stop() error {
	s.ls.Info(log_service.LogEvent{
		Message: "Stopping replicated server",
	})
	
	s.cancel()
	err := s.comm.Stop()
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to stop server",
			Metadata: map[string]any{"error": err.Error()},
		})
		return err
	}
	
	s.ls.Info(log_service.LogEvent{
		Message: "Replicated server stopped successfully",
	})
	return nil
}

func (s *ReplicatedServer) RegisterTypedHandler(msgType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	s.ls.Debug(log_service.LogEvent{
		Message: "Registering typed handler",
		Metadata: map[string]any{"messageType": msgType, "payloadType": payloadType.String()},
	})
	
	s.typedHandlers[msgType] = &TypedHandler{
		Handler:     handler,
		PayloadType: payloadType,
	}
}

func (s *ReplicatedServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	s.ls.Debug(log_service.LogEvent{
		Message: "Processing message",
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
						"expected": typedHandler.PayloadType.String(),
						"actual": actualType.String(),
					},
				})
				return &communication.Response{
					Code: communication.CodeBadRequest,
					Body: []byte(fmt.Sprintf("Invalid payload type for %s: expected %s, got %s", msg.Type, typedHandler.PayloadType, actualType)),
				}, nil
			}
		}
		return typedHandler.Handler(msg)
	}

	s.ls.Warn(log_service.LogEvent{
		Message: "No handler registered for message type",
		Metadata: map[string]any{"type": msg.Type},
	})
	
	return &communication.Response{
		Code: communication.CodeBadRequest,
		Body: []byte(fmt.Sprintf("No handler registered for message type: %s", msg.Type)),
	}, nil
}

func (s *ReplicatedServer) HandleStoreFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreFileRequest)
	
	s.ls.Info(log_service.LogEvent{
		Message: "Storing file",
		Metadata: map[string]any{"path": request.Path, "size": len(request.Data)},
	})

	err := s.fs.StoreFile(request.Path, request.Data)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to store file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to store file: %v", err)),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "File stored successfully",
		Metadata: map[string]any{"path": request.Path},
	})
	
	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *ReplicatedServer) HandleReadFileMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.ReadFileRequest)
	
	s.ls.Info(log_service.LogEvent{
		Message: "Reading file",
		Metadata: map[string]any{"path": request.Path},
	})

	data, err := s.fs.ReadFile(request.Path)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to read file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to read file: %v", err)),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "File read successfully",
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
		Message: "Deleting file",
		Metadata: map[string]any{"path": request.Path},
	})

	err := s.fs.DeleteFile(request.Path)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to delete file",
			Metadata: map[string]any{"path": request.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to delete file: %v", err)),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "File deleted successfully",
		Metadata: map[string]any{"path": request.Path},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *ReplicatedServer) HandleStoreChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreChunkRequest)
	
	s.ls.Info(log_service.LogEvent{
		Message: "Storing chunk",
		Metadata: map[string]any{"chunkID": request.ChunkID, "size": len(request.Data)},
	})

	err := s.cs.WriteChunk(request.ChunkID, request.Data)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to store chunk",
			Metadata: map[string]any{"chunkID": request.ChunkID, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to store chunk: %v", err)),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Chunk stored successfully",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *ReplicatedServer) HandleReadChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.ReadChunkRequest)
	
	s.ls.Info(log_service.LogEvent{
		Message: "Reading chunk",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	data, err := s.cs.ReadChunk(request.ChunkID)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to read chunk",
			Metadata: map[string]any{"chunkID": request.ChunkID, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to read chunk: %v", err)),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Chunk read successfully",
		Metadata: map[string]any{"chunkID": request.ChunkID, "size": len(data)},
	})

	return &communication.Response{
		Code: communication.CodeOK,
		Body: data,
	}, nil
}

func (s *ReplicatedServer) HandleDeleteChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.DeleteChunkRequest)
	
	s.ls.Info(log_service.LogEvent{
		Message: "Deleting chunk",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	err := s.cs.DeleteChunk(request.ChunkID)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to delete chunk",
			Metadata: map[string]any{"chunkID": request.ChunkID, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to delete chunk: %v", err)),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Chunk deleted successfully",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *ReplicatedServer) HandleStoreMetadataMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreMetadataRequest)
	metadata := request.Metadata
	
	s.ls.Info(log_service.LogEvent{
		Message: "Storing metadata",
		Metadata: map[string]any{"path": metadata.Path, "size": metadata.Size, "chunks": len(metadata.Chunks)},
	})

	err := s.ms.CreateFileMetadata(metadata.Path, metadata.Size, metadata.Chunks)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message: "Failed to store metadata",
			Metadata: map[string]any{"path": metadata.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(fmt.Sprintf("Failed to store metadata: %v", err)),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Metadata stored successfully",
		Metadata: map[string]any{"path": metadata.Path},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}
