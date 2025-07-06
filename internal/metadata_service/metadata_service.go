package metadata_service

import (
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
)

type FileMetadata struct {
	FileID      string
	Path        string
	Size        int64
	CreatedAt   time.Time
	ModifiedAt  time.Time
	Permissions string
	Chunks      []chunk_service.FileChunk
	Replicas    []MetadataReplica
}

type MetadataReplica struct {
	NodeID    string
	Address   string
	CreatedAt time.Time
}

type MetadataService interface {
	CreateFileMetadata(path string, size int64, chunks []chunk_service.FileChunk) error
	GetFileMetadata(path string) (*FileMetadata, error)
	DeleteFileMetadata(path string) error
	ListDirectory(path string) ([]FileMetadata, error)
}
