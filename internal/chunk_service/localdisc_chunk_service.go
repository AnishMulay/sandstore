package chunk_service

import (
	"os"
	"path/filepath"
)

type LocalDiscChunkService struct {
	baseDir string
}

func NewLocalDiscChunkService(baseDir string) *LocalDiscChunkService {
	if err := os.MkdirAll(baseDir, os.ModePerm); err != nil {
		panic(err)
	}
	return &LocalDiscChunkService{baseDir: baseDir}
}

func (cs *LocalDiscChunkService) chunkPath(chunkID string) string {
	return filepath.Join(cs.baseDir, chunkID+".chunk")
}
