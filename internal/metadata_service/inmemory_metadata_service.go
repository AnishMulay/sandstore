package metadata_service

import (
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type InMemoryMetadataService struct {
	mu    sync.RWMutex
	files map[string]*FileMetadata
	ls    log_service.LogService
}

func NewInMemoryMetadataService(ls log_service.LogService) *InMemoryMetadataService {
	return &InMemoryMetadataService{
		files: make(map[string]*FileMetadata),
		ls:    ls,
	}
}

func (ms *InMemoryMetadataService) CreateFileMetadataFromStruct(metadata FileMetadata) error {
	ms.ls.Info(log_service.LogEvent{
		Message: "Creating file metadata from struct",
		Metadata: map[string]any{
			"path":   metadata.Path,
			"fileID": metadata.FileID,
			"size":   metadata.Size,
			"chunks": len(metadata.Chunks),
		},
	})

	// Validate the metadata
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
		return ErrFileAlreadyExists
	}

	// Make a copy to avoid external mutations
	file := &FileMetadata{
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

// CreateFileMetadata is deprecated and will be removed in future versions.
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
		return ErrFileAlreadyExists
	}

	file := &FileMetadata{
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

func (ms *InMemoryMetadataService) GetFileMetadata(path string) (*FileMetadata, error) {
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
		return nil, ErrFileNotFound
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
		return ErrFileNotFound
	}

	delete(ms.files, path)

	ms.ls.Info(log_service.LogEvent{
		Message:  "File metadata deleted successfully",
		Metadata: map[string]any{"path": path},
	})

	return nil
}

func (ms *InMemoryMetadataService) ListDirectory(path string) ([]FileMetadata, error) {
	ms.ls.Debug(log_service.LogEvent{
		Message:  "Listing directory",
		Metadata: map[string]any{"path": path},
	})

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var files []FileMetadata
	for _, file := range ms.files {
		if path == "/" {
			files = append(files, *file)
		} else if file.Path == path || (len(file.Path) > len(path) && file.Path[:len(path)] == path && file.Path[len(path):][0] == '/') {
			files = append(files, *file)
		}
	}

	ms.ls.Debug(log_service.LogEvent{
		Message:  "Directory listed successfully",
		Metadata: map[string]any{"path": path, "files": len(files)},
	})

	return files, nil
}

func (ms *InMemoryMetadataService) UpdateFileMetadata(path string, metadata FileMetadata) error {
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
		return ErrFileNotFound
	}

	// Update the file metadata
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
