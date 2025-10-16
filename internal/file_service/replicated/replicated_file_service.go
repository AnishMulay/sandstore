package replicated

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_replicator"
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	fsinternal "github.com/AnishMulay/sandstore/internal/file_service/internal"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/google/uuid"
)

type ReplicatedFileService struct {
	ms        metadata_service.MetadataService
	cs        chunk_service.ChunkService
	cr        chunk_replicator.ChunkReplicator
	mr        metadata_replicator.MetadataReplicator
	ls        log_service.LogService
	chunkSize int64
}

func NewReplicatedFileService(ms metadata_service.MetadataService, cs chunk_service.ChunkService, cr chunk_replicator.ChunkReplicator, mr metadata_replicator.MetadataReplicator, ls log_service.LogService, chunkSize int64) *ReplicatedFileService {
	return &ReplicatedFileService{
		ms:        ms,
		cs:        cs,
		cr:        cr,
		mr:        mr,
		ls:        ls,
		chunkSize: chunkSize,
	}
}

func (fs *ReplicatedFileService) StoreFile(path string, data []byte) error {
	fs.ls.Info(log_service.LogEvent{
		Message:  "Storing file",
		Metadata: map[string]any{"path": path, "size": len(data)},
	})

	var chunks []chunk_service.FileChunk
	offset := 0
	now := time.Now()
	fileID := uuid.New().String()

	counter := 0

	for offset < len(data) {
		end := offset + int(fs.chunkSize)

		if end > len(data) {
			end = len(data)
		}

		chunkData := data[offset:end]
		chunkID := uuid.New().String()
		checksumRaw := sha256.Sum256(chunkData)
		checksum := fmt.Sprintf("%x", checksumRaw)

		err := fs.cs.WriteChunk(chunkID, chunkData)
		if err != nil {
			fs.ls.Error(log_service.LogEvent{
				Message:  "Failed to store chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunkID, "error": err.Error()},
			})
			return fsinternal.ErrChunkStoreFailed
		}

		replicas, err := fs.cr.ReplicateChunk(chunkID, chunkData, 2) // Assuming replication factor of 3
		if err != nil {
			fs.ls.Error(log_service.LogEvent{
				Message:  "Failed to replicate chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunkID, "error": err.Error()},
			})
			return fsinternal.ErrChunkReplicationFailed
		}

		chunks = append(chunks, chunk_service.FileChunk{
			ChunkID:    chunkID,
			FileID:     fileID,
			Size:       int64(len(chunkData)),
			CreatedAt:  now,
			ModifiedAt: now,
			Checksum:   checksum,
			Replicas:   replicas,
		})

		counter++
		offset = end
		if counter >= 5 {
			select {}
		}
	}

	fs.ls.Debug(log_service.LogEvent{
		Message:  "Creating file metadata",
		Metadata: map[string]any{"path": path, "chunks": len(chunks)},
	})

	metadata := metadata_service.NewFileMetadata(path, int64(len(data)), chunks)
	err := fs.ms.CreateFileMetadataFromStruct(metadata)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to create file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return fsinternal.ErrMetadataCreateFailed
	}

	fs.ls.Debug(log_service.LogEvent{
		Message:  "Replicating metadata",
		Metadata: map[string]any{"path": path},
	})

	op := metadata_replicator.MetadataReplicationOp{
		Type:     metadata_replicator.CREATE,
		Metadata: metadata,
	}
	err = fs.mr.Replicate(op)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to replicate metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return fsinternal.ErrMetadataReplicationFailed
	}

	fs.ls.Info(log_service.LogEvent{
		Message:  "File stored successfully",
		Metadata: map[string]any{"path": path, "chunks": len(chunks)},
	})

	return nil
}

func (fs *ReplicatedFileService) ReadFile(path string) ([]byte, error) {
	fs.ls.Info(log_service.LogEvent{
		Message:  "Reading file",
		Metadata: map[string]any{"path": path},
	})

	metadata, err := fs.ms.GetFileMetadata(path)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to get file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return nil, fsinternal.ErrMetadataGetFailed
	}

	var data []byte
	for _, chunk := range metadata.Chunks {
		chunkData, err := fs.cs.ReadChunk(chunk.ChunkID)
		if err != nil {
			fs.ls.Warn(log_service.LogEvent{
				Message:  "Chunk not found locally, fetching from replicas",
				Metadata: map[string]any{"path": path, "chunkID": chunk.ChunkID},
			})
			chunkData, err = fs.cr.FetchReplicatedChunk(chunk.ChunkID, chunk.Replicas)
			if err != nil {
				fs.ls.Error(log_service.LogEvent{
					Message:  "Failed to fetch replicated chunk",
					Metadata: map[string]any{"path": path, "chunkID": chunk.ChunkID, "error": err.Error()},
				})
				return nil, fsinternal.ErrReplicatedChunkFetchFailed
			}
		}

		data = append(data, chunkData...)
	}

	fs.ls.Info(log_service.LogEvent{
		Message:  "File read successfully",
		Metadata: map[string]any{"path": path, "size": len(data), "chunks": len(metadata.Chunks)},
	})

	return data, nil
}

func (fs *ReplicatedFileService) DeleteFile(path string) error {
	fs.ls.Info(log_service.LogEvent{
		Message:  "Deleting file",
		Metadata: map[string]any{"path": path},
	})

	metadata, err := fs.ms.GetFileMetadata(path)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to get file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return fsinternal.ErrMetadataGetFailed
	}

	for _, chunk := range metadata.Chunks {
		err = fs.cr.DeleteReplicatedChunk(chunk.ChunkID, chunk.Replicas)
		if err != nil {
			fs.ls.Error(log_service.LogEvent{
				Message:  "Failed to delete replicated chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunk.ChunkID, "error": err.Error()},
			})
			return fsinternal.ErrReplicatedChunkDeleteFailed
		}

		err = fs.cs.DeleteChunk(chunk.ChunkID)
		if err != nil {
			fs.ls.Error(log_service.LogEvent{
				Message:  "Failed to delete chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunk.ChunkID, "error": err.Error()},
			})
			return fsinternal.ErrChunkDeleteFailed
		}
	}

	fs.ls.Debug(log_service.LogEvent{
		Message:  "Replicating metadata deletion",
		Metadata: map[string]any{"path": path},
	})

	op := metadata_replicator.MetadataReplicationOp{
		Type:     metadata_replicator.DELETE,
		Metadata: *metadata,
	}
	err = fs.mr.Replicate(op)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to replicate metadata deletion",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return fsinternal.ErrMetadataReplicationFailed
	}

	fs.ls.Debug(log_service.LogEvent{
		Message:  "Deleting file metadata",
		Metadata: map[string]any{"path": path},
	})

	err = fs.ms.DeleteFileMetadata(path)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to delete file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return fsinternal.ErrMetadataDeleteFailed
	}

	fs.ls.Info(log_service.LogEvent{
		Message:  "File deleted successfully",
		Metadata: map[string]any{"path": path, "chunks": len(metadata.Chunks)},
	})

	return nil
}
