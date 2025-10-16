package localdisc

import (
	"os"
	"path/filepath"

	"github.com/AnishMulay/sandstore/internal/chunk_service/internal"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type LocalDiscChunkService struct {
	baseDir string
	ls      log_service.LogService
}

func NewLocalDiscChunkService(baseDir string, ls log_service.LogService) *LocalDiscChunkService {
	if err := os.MkdirAll(baseDir, os.ModePerm); err != nil {
		panic(err)
	}
	return &LocalDiscChunkService{
		baseDir: baseDir,
		ls:      ls,
	}
}

func (cs *LocalDiscChunkService) chunkPath(chunkID string) string {
	return filepath.Join(cs.baseDir, chunkID+".chunk")
}

func (cs *LocalDiscChunkService) WriteChunk(chunkID string, data []byte) error {
	cs.ls.Info(log_service.LogEvent{
		Message:  "Writing chunk",
		Metadata: map[string]any{"chunkID": chunkID, "size": len(data)},
	})

	path := cs.chunkPath(chunkID)
	err := os.WriteFile(path, data, os.ModePerm)
	if err != nil {
		cs.ls.Error(log_service.LogEvent{
			Message:  "Failed to write chunk",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})
		return internal.ErrChunkWriteFailed
	}

	cs.ls.Info(log_service.LogEvent{
		Message:  "Chunk written successfully",
		Metadata: map[string]any{"chunkID": chunkID},
	})
	return nil
}

func (cs *LocalDiscChunkService) ReadChunk(chunkID string) ([]byte, error) {
	cs.ls.Info(log_service.LogEvent{
		Message:  "Reading chunk",
		Metadata: map[string]any{"chunkID": chunkID},
	})

	path := cs.chunkPath(chunkID)
	data, err := os.ReadFile(path)
	if err != nil {
		cs.ls.Error(log_service.LogEvent{
			Message:  "Failed to read chunk",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})
		return nil, internal.ErrChunkReadFailed
	}

	cs.ls.Info(log_service.LogEvent{
		Message:  "Chunk read successfully",
		Metadata: map[string]any{"chunkID": chunkID, "size": len(data)},
	})
	return data, nil
}

func (cs *LocalDiscChunkService) DeleteChunk(chunkID string) error {
	cs.ls.Info(log_service.LogEvent{
		Message:  "Deleting chunk",
		Metadata: map[string]any{"chunkID": chunkID},
	})

	path := cs.chunkPath(chunkID)
	err := os.Remove(path)
	if err != nil {
		cs.ls.Error(log_service.LogEvent{
			Message:  "Failed to delete chunk",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})
		return internal.ErrChunkDeleteFailed
	}

	cs.ls.Info(log_service.LogEvent{
		Message:  "Chunk deleted successfully",
		Metadata: map[string]any{"chunkID": chunkID},
	})
	return nil
}
