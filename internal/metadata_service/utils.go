package metadata_service

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
)

func GenerateFileID(path string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s_%d", path, time.Now().UnixNano())))
	return fmt.Sprintf("%x", hash[:8]) // Use first 8 bytes of hash
}

func NewFileMetadata(path string, size int64, chunks []chunk_service.FileChunk) FileMetadata {
	now := time.Now()
	return FileMetadata{
		FileID:      GenerateFileID(path),
		Path:        path,
		Size:        size,
		CreatedAt:   now,
		ModifiedAt:  now,
		Permissions: "rw-r--r--",
		Chunks:      chunks,
	}
}
