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
	CreateFile(path string, size int64) error
	GetFile(path string) (*FileMetadata, error)
	DeleteFile(path string) error
	ListDirectory(path string) ([]FileMetadata, error)
}
