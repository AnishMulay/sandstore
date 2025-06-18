package metadata

import (
	"sync"
	"time"
)

type InMemoryMetadataService struct {
	mu    sync.RWMutex
	files map[string]*FileMetadata
}

func NewInMemoryMetadataService() *InMemoryMetadataService {
	return &InMemoryMetadataService{
		files: make(map[string]*FileMetadata),
	}
}

func (ms *InMemoryMetadataService) CreateFileMetadata(path string, size int64) error {
	ms.mu.RLock()
	_, exists := ms.files[path]
	ms.mu.RUnlock()

	if exists {
		return ErrFileAlreadyExists
	}

	file := &FileMetadata{
		Path:        path,
		Size:        size,
		CreatedAt:   time.Now(),
		ModifiedAt:  time.Now(),
		Permissions: "rw-r--r--",
	}

	ms.mu.Lock()
	ms.files[path] = file
	ms.mu.Unlock()

	return nil
}

func (ms *InMemoryMetadataService) GetFileMetadata(path string) (*FileMetadata, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	file, exists := ms.files[path]
	if !exists {
		return nil, ErrFileNotFound
	}

	return file, nil
}
