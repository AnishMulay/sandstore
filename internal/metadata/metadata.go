package metadata

import "time"

type FileMetadata struct {
	Path        string
	Size        int64
	CreatedAt   time.Time
	ModifiedAt  time.Time
	Permissions string
}

type MetadataService interface {
	CreateFileMetadata(path string, size int64) error
	GetFileMetadata(path string) (*FileMetadata, error)
	DeleteFile(path string) error
	ListDirectory(path string) ([]FileMetadata, error)
}
