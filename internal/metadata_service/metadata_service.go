package metadata_service

import (
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	errorsinternal "github.com/AnishMulay/sandstore/internal/metadata_service/internal"
)

type FileMetadata struct {
	FileID      string
	Path        string
	Size        int64
	CreatedAt   time.Time
	ModifiedAt  time.Time
	Permissions string
	Chunks      []chunk_service.FileChunk
}

type MetadataService interface {
	CreateFileMetadataFromStruct(metadata FileMetadata) error

	GetFileMetadata(path string) (*FileMetadata, error)
	DeleteFileMetadata(path string) error
	ListDirectory(path string) ([]FileMetadata, error)
	UpdateFileMetadata(path string, metadata FileMetadata) error
}

func (fm FileMetadata) Validate() error {
	if fm.Path == "" {
		return errorsinternal.ErrInvalidPath
	}
	if fm.FileID == "" {
		return errorsinternal.ErrMissingFileID
	}
	if fm.Size < 0 {
		return errorsinternal.ErrInvalidSize
	}
	if fm.CreatedAt.IsZero() {
		return errorsinternal.ErrMissingCreatedAt
	}
	if fm.ModifiedAt.IsZero() {
		return errorsinternal.ErrMissingModifiedAt
	}
	if fm.Permissions == "" {
		return errorsinternal.ErrMissingPermissions
	}
	return nil
}
