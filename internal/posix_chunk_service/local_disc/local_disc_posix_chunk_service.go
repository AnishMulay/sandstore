package localdisc

import (
	"os"
	"path/filepath"

	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/posix_chunk_replicator"
	csinternal "github.com/AnishMulay/sandstore/internal/posix_chunk_service/internal"
)

type PosixLocalDiscChunkService struct {
	baseDir           string
	ls                log_service.LogService
	replicationFactor int
	replicator        posix_chunk_replicator.ChunkReplicator
}

func NewPosixLocalDiscChunkService(
	baseDir string,
	ls log_service.LogService,
	replicator posix_chunk_replicator.ChunkReplicator,
	replicationFactor int,
) *PosixLocalDiscChunkService {
	if err := os.MkdirAll(baseDir, os.ModePerm); err != nil {
		panic(err)
	}
	return &PosixLocalDiscChunkService{
		baseDir:           baseDir,
		ls:                ls,
		replicator:        replicator,
		replicationFactor: replicationFactor,
	}
}

func (cs *PosixLocalDiscChunkService) chunkPath(chunkID string) string {
	return filepath.Join(cs.baseDir, chunkID+".chunk")
}

func (cs *PosixLocalDiscChunkService) WriteChunk(chunkID string, data []byte) error {
	cs.ls.Info(log_service.LogEvent{
		Message:  "Writing chunk (POSIX)",
		Metadata: map[string]any{"chunkID": chunkID, "size": len(data)},
	})

	path := cs.chunkPath(chunkID)

	// 1. Write to local disk
	// We write locally first to fail fast if disk is full/broken.
	err := os.WriteFile(path, data, 0644) // Use 0644 for clearer permissions
	if err != nil {
		cs.ls.Error(log_service.LogEvent{
			Message:  "Failed to write chunk locally",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})
		return csinternal.ErrChunkWriteFailed
	}

	// 2. Synchronous Replication
	err = cs.replicator.ReplicateChunk(chunkID, data, cs.replicationFactor)
	if err != nil {
		cs.ls.Error(log_service.LogEvent{
			Message:  "Replication failed, rolling back local write",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})
		
		// ROLLBACK: Delete the local file so we don't have inconsistent state
		// (Data existing locally but considered "failed" by the client)
		_ = os.Remove(path)
		
		return csinternal.ErrChunkWriteFailed
	}

	cs.ls.Info(log_service.LogEvent{
		Message:  "Chunk written and replicated successfully",
		Metadata: map[string]any{"chunkID": chunkID},
	})
	return nil
}

func (cs *PosixLocalDiscChunkService) ReadChunk(chunkID string) ([]byte, error) {
	// 1. Try local read first
	path := cs.chunkPath(chunkID)
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}

	// 2. Fallback to replicas (Read Repair opportunity)
	cs.ls.Warn(log_service.LogEvent{
		Message:  "Chunk not found locally, fetching from replicas",
		Metadata: map[string]any{"chunkID": chunkID, "localError": err.Error()},
	})

	data, err = cs.replicator.FetchReplicatedChunk(chunkID)
	if err != nil {
		return nil, csinternal.ErrChunkReadFailed
	}

	// OPTIONAL: Read Repair (Self-Healing)
	// If we fetched it from a neighbor, save it locally so next time it's fast.
	go func() {
		_ = os.WriteFile(path, data, 0644)
		cs.ls.Info(log_service.LogEvent{
			Message: "Performed read-repair on chunk",
			Metadata: map[string]any{"chunkID": chunkID},
		})
	}()

	return data, nil
}

func (cs *PosixLocalDiscChunkService) DeleteChunk(chunkID string) error {
	// We attempt both deletions regardless of errors to ensure eventual consistency
	
	// 1. Delete local
	path := cs.chunkPath(chunkID)
	localErr := os.Remove(path)
	if localErr != nil && !os.IsNotExist(localErr) {
		cs.ls.Warn(log_service.LogEvent{
			Message: "Failed to delete local chunk", 
			Metadata: map[string]any{"chunkID": chunkID, "error": localErr.Error()},
		})
	}

	// 2. Delete replicas
	replicaErr := cs.replicator.DeleteReplicatedChunk(chunkID)
	if replicaErr != nil {
		cs.ls.Warn(log_service.LogEvent{
			Message: "Failed to delete replicated chunks", 
			Metadata: map[string]any{"chunkID": chunkID, "error": replicaErr.Error()},
		})
	}

	if localErr != nil || replicaErr != nil {
		return csinternal.ErrChunkDeleteFailed
	}
	return nil
}