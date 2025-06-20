package metadata_service

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

func (ms *InMemoryMetadataService) DeleteFileMetadata(path string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.files[path]; !exists {
		return ErrFileNotFound
	}

	delete(ms.files, path)

	return nil
}

func (ms *InMemoryMetadataService) ListDirectory(path string) ([]FileMetadata, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var files []FileMetadata
	for _, file := range ms.files {
		if file.Path == path || (len(file.Path) > len(path) && file.Path[:len(path)] == path && file.Path[len(path):][0] == '/') {
			files = append(files, *file)
		}
	}

	return files, nil
}
