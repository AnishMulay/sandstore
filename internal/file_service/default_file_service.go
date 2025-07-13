package file_service

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/google/uuid"
)

type DefaultFileService struct {
	ms        metadata_service.MetadataService
	cs        chunk_service.ChunkService
	ls        log_service.LogService
	chunkSize int64
}

func NewDefaultFileService(ms metadata_service.MetadataService, cs chunk_service.ChunkService, ls log_service.LogService, chunkSize int64) *DefaultFileService {
	return &DefaultFileService{
		ms:        ms,
		cs:        cs,
		ls:        ls,
		chunkSize: chunkSize,
	}
}

func (fs *DefaultFileService) StoreFile(path string, data []byte) error {
	fs.ls.Info(log_service.LogEvent{
		Message: "Storing file",
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
				Message: "Failed to store chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunkID, "error": err.Error()},
			})
			return ErrChunkStoreFailed
		}

		chunks = append(chunks, chunk_service.FileChunk{
			ChunkID:    chunkID,
			FileID:     fileID,
			Size:       int64(len(chunkData)),
			CreatedAt:  now,
			ModifiedAt: now,
			Checksum:   checksum,
		})

		counter++
		offset = end
		if counter >= 5 {
			select {}
		}
	}

	fs.ls.Debug(log_service.LogEvent{
		Message: "Creating file metadata",
		Metadata: map[string]any{"path": path, "chunks": len(chunks)},
	})
	
	err := fs.ms.CreateFileMetadata(path, int64(len(data)), chunks)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message: "Failed to create file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return ErrMetadataCreateFailed
	}
	
	fs.ls.Info(log_service.LogEvent{
		Message: "File stored successfully",
		Metadata: map[string]any{"path": path, "chunks": len(chunks)},
	})
	
	return nil
}

func (fs *DefaultFileService) ReadFile(path string) ([]byte, error) {
	fs.ls.Info(log_service.LogEvent{
		Message: "Reading file",
		Metadata: map[string]any{"path": path},
	})
	
	metadata, err := fs.ms.GetFileMetadata(path)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message: "Failed to get file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return nil, ErrMetadataGetFailed
	}

	var data []byte
	for _, chunk := range metadata.Chunks {
		chunkData, err := fs.cs.ReadChunk(chunk.ChunkID)
		if err != nil {
			fs.ls.Error(log_service.LogEvent{
				Message: "Failed to read chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunk.ChunkID, "error": err.Error()},
			})
			return nil, ErrChunkReadFailed
		}

		data = append(data, chunkData...)
	}

	fs.ls.Info(log_service.LogEvent{
		Message: "File read successfully",
		Metadata: map[string]any{"path": path, "size": len(data), "chunks": len(metadata.Chunks)},
	})

	return data, nil
}

func (fs *DefaultFileService) DeleteFile(path string) error {
	fs.ls.Info(log_service.LogEvent{
		Message: "Deleting file",
		Metadata: map[string]any{"path": path},
	})
	
	metadata, err := fs.ms.GetFileMetadata(path)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message: "Failed to get file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return ErrMetadataGetFailed
	}

	for _, chunk := range metadata.Chunks {
		err = fs.cs.DeleteChunk(chunk.ChunkID)
		if err != nil {
			fs.ls.Error(log_service.LogEvent{
				Message: "Failed to delete chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunk.ChunkID, "error": err.Error()},
			})
			return ErrChunkDeleteFailed
		}
	}

	fs.ls.Debug(log_service.LogEvent{
		Message: "Deleting file metadata",
		Metadata: map[string]any{"path": path},
	})
	
	err = fs.ms.DeleteFileMetadata(path)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message: "Failed to delete file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return ErrMetadataDeleteFailed
	}

	fs.ls.Info(log_service.LogEvent{
		Message: "File deleted successfully",
		Metadata: map[string]any{"path": path, "chunks": len(metadata.Chunks)},
	})
	
	return nil
}
