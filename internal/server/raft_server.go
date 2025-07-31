package server

import (
	"context"
	"reflect"
	"sync"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
)

type RaftServer struct {
	comm           communication.Communicator
	fs             file_service.FileService
	cs             chunk_service.ChunkService
	ms             metadata_service.MetadataService
	ls             log_service.LogService
	ctx            context.Context
	cancel         context.CancelFunc
	typedHandlers  map[string]*TypedHandler
	clusterService cluster_service.ClusterService
	stopped        bool
	stopMutex      sync.RWMutex
}

func NewRaftServer(comm communication.Communicator, fs file_service.FileService, cs chunk_service.ChunkService, ms metadata_service.MetadataService, ls log_service.LogService, clusterService cluster_service.ClusterService) *RaftServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &RaftServer{
		comm:           comm,
		fs:             fs,
		cs:             cs,
		ms:             ms,
		ls:             ls,
		ctx:            ctx,
		cancel:         cancel,
		typedHandlers:  make(map[string]*TypedHandler),
		clusterService: clusterService,
		stopped:        false,
	}
}

func (s *RaftServer) Start() error {
	s.ls.Info(log_service.LogEvent{
		Message: "Starting raft server",
	})

	err := s.comm.Start(s.handleMessage)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to start server",
			Metadata: map[string]any{"error": err.Error()},
		})
		return ErrServerStartFailed
	}

	// Start the Raft cluster service
	if raftCluster, ok := s.clusterService.(*cluster_service.RaftClusterService); ok {
		raftCluster.Start()
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Raft server started successfully",
	})
	return nil
}

func (s *RaftServer) Stop() error {
	s.stopMutex.Lock()
	defer s.stopMutex.Unlock()

	if s.stopped {
		s.ls.Debug(log_service.LogEvent{
			Message: "Server already stopped, skipping",
		})
		return nil
	}

	s.ls.Info(log_service.LogEvent{
		Message: "Stopping raft server",
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

	s.stopped = true
	s.ls.Info(log_service.LogEvent{
		Message: "raft server stopped successfully",
	})
	return nil
}

func (s *RaftServer) RegisterTypedHandler(msgType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	s.ls.Debug(log_service.LogEvent{
		Message:  "Registering typed handler",
		Metadata: map[string]any{"messageType": msgType, "payloadType": payloadType.String()},
	})

	s.typedHandlers[msgType] = &TypedHandler{
		Handler:     handler,
		PayloadType: payloadType,
	}
}

func (s *RaftServer) handleMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *RaftServer) HandleStoreFileMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *RaftServer) HandleReadFileMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *RaftServer) HandleDeleteFileMessage(msg communication.Message) (*communication.Response, error) {
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

func (s *RaftServer) HandleStoreChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreChunkRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Storing chunk",
		Metadata: map[string]any{"chunkID": request.ChunkID, "size": len(request.Data)},
	})

	err := s.cs.WriteChunk(request.ChunkID, request.Data)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to store chunk",
			Metadata: map[string]any{"chunkID": request.ChunkID, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(ErrChunkStoreFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "Chunk stored successfully",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *RaftServer) HandleReadChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.ReadChunkRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Reading chunk",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	data, err := s.cs.ReadChunk(request.ChunkID)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to read chunk",
			Metadata: map[string]any{"chunkID": request.ChunkID, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(ErrChunkReadFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "Chunk read successfully",
		Metadata: map[string]any{"chunkID": request.ChunkID, "size": len(data)},
	})

	return &communication.Response{
		Code: communication.CodeOK,
		Body: data,
	}, nil
}

func (s *RaftServer) HandleDeleteChunkMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.DeleteChunkRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Deleting chunk",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	err := s.cs.DeleteChunk(request.ChunkID)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to delete chunk",
			Metadata: map[string]any{"chunkID": request.ChunkID, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(ErrChunkDeleteFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "Chunk deleted successfully",
		Metadata: map[string]any{"chunkID": request.ChunkID},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *RaftServer) HandleStoreMetadataMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.StoreMetadataRequest)
	metadata := request.Metadata

	s.ls.Info(log_service.LogEvent{
		Message:  "Storing metadata",
		Metadata: map[string]any{"path": metadata.Path, "size": metadata.Size, "chunks": len(metadata.Chunks)},
	})

	err := s.ms.CreateFileMetadata(metadata.Path, metadata.Size, metadata.Chunks)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to store metadata",
			Metadata: map[string]any{"path": metadata.Path, "error": err.Error()},
		})
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte(ErrMetadataStoreFailed.Error()),
		}, nil
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "Metadata stored successfully",
		Metadata: map[string]any{"path": metadata.Path},
	})

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *RaftServer) HandleStopServerMessage(msg communication.Message) (*communication.Response, error) {
	s.ls.Info(log_service.LogEvent{
		Message: "Received stop server message",
	})

	// Stop the server in a goroutine to avoid blocking the response
	go func() {
		if err := s.Stop(); err != nil {
			s.ls.Error(log_service.LogEvent{
				Message:  "Failed to stop server via message",
				Metadata: map[string]any{"error": err.Error()},
			})
		}
	}()

	return &communication.Response{
		Code: communication.CodeOK,
	}, nil
}

func (s *RaftServer) HandleRequestVoteMessage(msg communication.Message) (*communication.Response, error) {
	request := msg.Payload.(communication.RequestVoteRequest)

	s.ls.Info(log_service.LogEvent{
		Message:  "Handling request vote",
		Metadata: map[string]any{"term": request.Term, "candidateID": request.CandidateID},
	})

	if raftCluster, ok := s.clusterService.(*cluster_service.RaftClusterService); ok {
		voteGranted, err := raftCluster.HandleRequestVote(request)
		if err != nil {
			return &communication.Response{
				Code: communication.CodeInternal,
				Body: []byte(err.Error()),
			}, nil
		}

		if voteGranted {
			return &communication.Response{
				Code: communication.CodeOK,
			}, nil
		} else {
			return &communication.Response{
				Code: communication.CodeBadRequest,
			}, nil
		}
	}

	return &communication.Response{
		Code: communication.CodeInternal,
		Body: []byte("cluster service not available"),
	}, nil
}
