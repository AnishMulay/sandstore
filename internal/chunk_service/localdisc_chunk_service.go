package chunk_service

import (
	"os"
	"path/filepath"

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
	path := cs.chunkPath(chunkID)
	return os.WriteFile(path, data, os.ModePerm)
}

func (cs *LocalDiscChunkService) ReadChunk(chunkID string) ([]byte, error) {
	path := cs.chunkPath(chunkID)
	return os.ReadFile(path)
}

func (cs *LocalDiscChunkService) DeleteChunk(chunkID string) error {
	path := cs.chunkPath(chunkID)
	return os.Remove(path)
}
