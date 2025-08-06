package file_service

import (
	"github.com/yourusername/yourproject/internal/log_service"
)

type RaftFileService struct {
	ls log_service.LogService
	mr metadata_replicator.RaftMetadataReplicator
	cs chunk_service.ChunkService
	ms metadata_service.MetadataService
	cr chunk_replicator.ChunkReplicator
	chunkSize int64
}

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

		replicas, err := fs.cr.ReplicateChunk(chunkID, chunkData, 2) // Assuming replication factor of 3
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

	metadata := metadata_service.NewFileMetadata(path, int64(len(data)), chunks)
	err := fs.ms.CreateFileMetadataFromStruct(metadata)
	if err != nil {
		fs.ls.Error(log_service.LogEvent{
			Message:  "Failed to create file metadata",
			Metadata: map[string]any{"path": path, "error": err.Error()},
		})
		return ErrMetadataCreateFailed
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
		return ErrMetadataReplicationFailed
	}

	fs.ls.Info(log_service.LogEvent{
		Message:  "File stored successfully",
		Metadata: map[string]any{"path": path, "chunks": len(chunks)},
	})

	return nil
}