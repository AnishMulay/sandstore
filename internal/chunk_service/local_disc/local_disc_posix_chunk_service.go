package localdisc

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/AnishMulay/sandstore/internal/chunk_replicator"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

var (
	// Chunk operation errors
	ErrChunkWriteFailed  = errors.New("failed to write chunk")
	ErrChunkReadFailed   = errors.New("failed to read chunk")
	ErrChunkDeleteFailed = errors.New("failed to delete chunk")
	ErrChunkNotFound     = errors.New("chunk not found")
)

type LocalDiscChunkService struct {
	baseDir           string
	ls                log_service.LogService
	replicationFactor int
	replicator        chunk_replicator.ChunkReplicator
}

func NewLocalDiscChunkService(
	baseDir string,
	ls log_service.LogService,
	replicator chunk_replicator.ChunkReplicator,
	replicationFactor int,
) *LocalDiscChunkService {
	if err := os.MkdirAll(baseDir, os.ModePerm); err != nil {
		panic(err)
	}
	return &LocalDiscChunkService{
		baseDir:           baseDir,
		ls:                ls,
		replicator:        replicator,
		replicationFactor: replicationFactor,
	}
}

func (cs *LocalDiscChunkService) chunkPath(chunkID string) string {
	return filepath.Join(cs.baseDir, chunkID+".chunk")
}

func (cs *LocalDiscChunkService) WriteChunkLocal(chunkID string, data []byte) error {
	path := cs.chunkPath(chunkID)

	if err := os.WriteFile(path, data, 0644); err != nil {
		cs.ls.Error(log_service.LogEvent{
			Message:  "Failed to write chunk locally",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})
		return ErrChunkWriteFailed
	}

	return nil
}

func (cs *LocalDiscChunkService) WriteChunk(chunkID string, data []byte) error {
	cs.ls.Info(log_service.LogEvent{
		Message:  "Writing chunk (POSIX)",
		Metadata: map[string]any{"chunkID": chunkID, "size": len(data)},
	})

	if err := cs.WriteChunkLocal(chunkID, data); err != nil {
		return err
	}

	err := cs.replicator.ReplicateChunk(chunkID, data, cs.replicationFactor)
	if err != nil {
		cs.ls.Error(log_service.LogEvent{
			Message:  "Replication failed, rolling back local write",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})

		_ = cs.DeleteChunkLocal(chunkID)

		return ErrChunkWriteFailed
	}

	cs.ls.Info(log_service.LogEvent{
		Message:  "Chunk written and replicated successfully",
		Metadata: map[string]any{"chunkID": chunkID},
	})
	return nil
}

func (cs *LocalDiscChunkService) ReadChunkLocal(chunkID string) ([]byte, error) {
	path := cs.chunkPath(chunkID)
	return os.ReadFile(path)
}

func (cs *LocalDiscChunkService) ReadChunk(chunkID string) ([]byte, error) {
	data, err := cs.ReadChunkLocal(chunkID)
	if err == nil {
		return data, nil
	}

	cs.ls.Warn(log_service.LogEvent{
		Message:  "Chunk not found locally, fetching from replicas",
		Metadata: map[string]any{"chunkID": chunkID, "localError": err.Error()},
	})

	data, err = cs.replicator.FetchReplicatedChunk(chunkID)
	if err != nil {
		return nil, ErrChunkReadFailed
	}

	// OPTIONAL: Read Repair (Self-Healing)
	// If we fetched it from a neighbor, save it locally so next time it's fast.
	go func() {
		if err := cs.WriteChunkLocal(chunkID, data); err == nil {
			cs.ls.Info(log_service.LogEvent{
				Message:  "Performed read-repair on chunk",
				Metadata: map[string]any{"chunkID": chunkID},
			})
		}
	}()

	return data, nil
}

func (cs *LocalDiscChunkService) DeleteChunkLocal(chunkID string) error {
	path := cs.chunkPath(chunkID)
	return os.Remove(path)
}

func (cs *LocalDiscChunkService) DeleteChunk(chunkID string) error {
	localErr := cs.DeleteChunkLocal(chunkID)
	if localErr != nil && !os.IsNotExist(localErr) {
		cs.ls.Warn(log_service.LogEvent{
			Message:  "Failed to delete local chunk",
			Metadata: map[string]any{"chunkID": chunkID, "error": localErr.Error()},
		})
	}

	// 2. Delete replicas
	replicaErr := cs.replicator.DeleteReplicatedChunk(chunkID)
	if replicaErr != nil {
		cs.ls.Warn(log_service.LogEvent{
			Message:  "Failed to delete replicated chunks",
			Metadata: map[string]any{"chunkID": chunkID, "error": replicaErr.Error()},
		})
	}

	if localErr != nil || replicaErr != nil {
		return ErrChunkDeleteFailed
	}
	return nil
}
