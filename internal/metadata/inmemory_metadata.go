package metadata

import "sync"

type InMemoryMetadataService struct {
	mu    sync.RWMutex
	files map[string]*FileMetadata
}
