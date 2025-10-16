package inmemory

import (
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	errorsinternal "github.com/AnishMulay/sandstore/internal/metadata_service/internal"
)

type InMemoryMetadataService struct {
	mu    sync.RWMutex
	files map[string]*metadata_service.FileMetadata
	ls    log_service.LogService
}

func NewInMemoryMetadataService(ls log_service.LogService) *InMemoryMetadataService {
	return &InMemoryMetadataService{
		files: make(map[string]*metadata_service.FileMetadata),
		ls:    ls,
	}
}

func (ms *InMemoryMetadataService) CreateFileMetadataFromStruct(metadata metadata_service.FileMetadata) error {
	ms.ls.Info(log_service.LogEvent{
		Message: "Creating file metadata from struct",
		Metadata: map[string]any{
			"path":   metadata.Path,
			"fileID": metadata.FileID,
			"size":   metadata.Size,
			"chunks": len(metadata.Chunks),
		},
	})

	if err := metadata.Validate(); err != nil {
		ms.ls.Error(log_service.LogEvent{
			Message:  "Invalid metadata provided",
			Metadata: map[string]any{"path": metadata.Path, "error": err.Error()},
		})
		return err
	}

	ms.mu.RLock()
	_, exists := ms.files[metadata.Path]
	ms.mu.RUnlock()

	if exists {
		ms.ls.Error(log_service.LogEvent{
			Message:  "File metadata already exists",
			Metadata: map[string]any{"path": metadata.Path},
		})
		return errorsinternal.ErrFileAlreadyExists
	}

	file := &metadata_service.FileMetadata{
		FileID:      metadata.FileID,
		Path:        metadata.Path,
		Size:        metadata.Size,
		CreatedAt:   metadata.CreatedAt,
		ModifiedAt:  metadata.ModifiedAt,
		Permissions: metadata.Permissions,
		Chunks:      make([]chunk_service.FileChunk, len(metadata.Chunks)),
	}
	copy(file.Chunks, metadata.Chunks)

	ms.mu.Lock()
	ms.files[metadata.Path] = file
	ms.mu.Unlock()

	ms.ls.Info(log_service.LogEvent{
		Message: "File metadata created successfully",
		Metadata: map[string]any{
			"path":   metadata.Path,
			"fileID": metadata.FileID,
			"chunks": len(metadata.Chunks),
		},
	})

	return nil
}

func (ms *InMemoryMetadataService) CreateFileMetadata(path string, size int64, chunks []chunk_service.FileChunk) error {
	ms.ls.Info(log_service.LogEvent{
		Message:  "Creating file metadata",
		Metadata: map[string]any{"path": path, "size": size, "chunks": len(chunks)},
	})

	ms.mu.RLock()
	_, exists := ms.files[path]
	ms.mu.RUnlock()

	if exists {
		ms.ls.Warn(log_service.LogEvent{
			Message:  "File metadata already exists",
			Metadata: map[string]any{"path": path},
		})
		return errorsinternal.ErrFileAlreadyExists
	}

	file := &metadata_service.FileMetadata{
		Path:        path,
		Size:        size,
		CreatedAt:   time.Now(),
		ModifiedAt:  time.Now(),
		Permissions: "rw-r--r--",
		Chunks:      chunks,
	}

	ms.mu.Lock()
	ms.files[path] = file
	ms.mu.Unlock()

	ms.ls.Info(log_service.LogEvent{
		Message:  "File metadata created successfully",
		Metadata: map[string]any{"path": path, "chunks": len(chunks)},
	})

	return nil
}

func (ms *InMemoryMetadataService) GetFileMetadata(path string) (*metadata_service.FileMetadata, error) {
	ms.ls.Debug(log_service.LogEvent{
		Message:  "Getting file metadata",
		Metadata: map[string]any{"path": path},
	})

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	file, exists := ms.files[path]
	if !exists {
		ms.ls.Warn(log_service.LogEvent{
			Message:  "File metadata not found",
			Metadata: map[string]any{"path": path},
		})
		return nil, errorsinternal.ErrFileNotFound
	}

	ms.ls.Debug(log_service.LogEvent{
		Message:  "File metadata retrieved successfully",
		Metadata: map[string]any{"path": path, "size": file.Size, "chunks": len(file.Chunks)},
	})

	return file, nil
}

func (ms *InMemoryMetadataService) DeleteFileMetadata(path string) error {
	ms.ls.Info(log_service.LogEvent{
		Message:  "Deleting file metadata",
		Metadata: map[string]any{"path": path},
	})

	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.files[path]; !exists {
		ms.ls.Warn(log_service.LogEvent{
			Message:  "File metadata not found for deletion",
			Metadata: map[string]any{"path": path},
		})
		return errorsinternal.ErrFileNotFound
	}

	delete(ms.files, path)

	ms.ls.Info(log_service.LogEvent{
		Message:  "File metadata deleted successfully",
		Metadata: map[string]any{"path": path},
	})

	return nil
}

func (ms *InMemoryMetadataService) ListDirectory(path string) ([]metadata_service.FileMetadata, error) {
	ms.ls.Debug(log_service.LogEvent{
		Message:  "Listing directory",
		Metadata: map[string]any{"path": path},
	})

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var files []metadata_service.FileMetadata
	for _, file := range ms.files {
		if path == "/" {
			files = append(files, *file)
		} else if file.Path == path || (len(file.Path) > len(path) && file.Path[:len(path)] == path && len(file.Path) > len(path) && file.Path[len(path)] == '/') {
			files = append(files, *file)
		}
	}

	ms.ls.Debug(log_service.LogEvent{
		Message:  "Directory listed successfully",
		Metadata: map[string]any{"path": path, "files": len(files)},
	})

	return files, nil
}

func (ms *InMemoryMetadataService) UpdateFileMetadata(path string, metadata metadata_service.FileMetadata) error {
	ms.ls.Info(log_service.LogEvent{
		Message:  "Updating file metadata",
		Metadata: map[string]any{"path": path, "size": metadata.Size, "chunks": len(metadata.Chunks)},
	})

	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.files[path]; !exists {
		ms.ls.Warn(log_service.LogEvent{
			Message:  "File metadata not found for update",
			Metadata: map[string]any{"path": path},
		})
		return errorsinternal.ErrFileNotFound
	}

	file := ms.files[path]
	file.Size = metadata.Size
	file.ModifiedAt = time.Now()
	file.Permissions = metadata.Permissions
	file.Chunks = metadata.Chunks
	ms.files[path] = file

	ms.ls.Info(log_service.LogEvent{
		Message:  "File metadata updated successfully",
		Metadata: map[string]any{"path": path, "chunks": len(metadata.Chunks)},
	})

	return nil
}

var _ metadata_service.MetadataService = (*InMemoryMetadataService)(nil)
