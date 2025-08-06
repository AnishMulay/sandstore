package file_service

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_replicator"
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/google/uuid"
)

type RaftFileService struct {
	ls        log_service.LogService
	mr        *metadata_replicator.RaftMetadataReplicator
	cs        chunk_service.ChunkService
	ms        metadata_service.MetadataService
	cr        chunk_replicator.ChunkReplicator
	chunkSize int64
}

// right now the chunks are created and replicated first. but this will also change once an update metadata op is added
func (fs *RaftFileService) StoreFile(path string, data []byte) error {
	fs.ls.Info(log_service.LogEvent{
		Message:  "Storing file (RaftFileService)",
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
			return ErrChunkStoreFailed
		}

		replicas, err := fs.cr.ReplicateChunk(chunkID, chunkData, 2)
		if err != nil {
			fs.ls.Error(log_service.LogEvent{
				Message:  "Failed to replicate chunk",
				Metadata: map[string]any{"path": path, "chunkID": chunkID, "error": err.Error()},
			})
			return ErrChunkReplicationFailed
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
		Message:  "Successfully stored all chunks",
		Metadata: map[string]any{"path": path, "fileID": fileID, "numChunks": len(chunks)},
	})

	// Create temporary metadata struct for Raft replication
	tempMetadata := metadata_service.NewFileMetadata(path, int64(len(data)), chunks)

	fs.ls.Debug(log_service.LogEvent{
		Message:  "Replicating metadata via Raft",
		Metadata: map[string]any{"path": path},
	})

	// Replicate via Raft first - only proceed if consensus is achieved
	op := metadata_replicator.MetadataReplicationOp{
		Type:     metadata_replicator.CREATE,
		Metadata: tempMetadata,
	}
	err := fs.mr.Replicate(op)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to replicate metadata via Raft",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return ErrMetadataReplicationFailed
	}

	fs.ls.Debug(log_service.LogEvent{
		Message:  "Raft replication successful, committing to local store",
		Metadata: map[string]any{"path": path},
	})

	// Only commit to local metadata store after Raft consensus
	err = fs.ms.CreateFileMetadataFromStruct(tempMetadata)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to commit file metadata to local store",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return ErrMetadataCreateFailed
	}

	fs.ls.Info(log_service.LogEvent{
		Message:  "File stored successfully via Raft",
		Metadata: map[string]any{"path": path, "chunks": len(chunks)},
	})

	return nil
}