package chunk_service

import "time"

type FileChunk struct {
	ChunkID    string
	FileID     string
	Size       int64
	CreatedAt  time.Time
	ModifiedAt time.Time
	Checksum   string
}

type ChunkService interface {
	WriteChunk(chunkID string, data []byte) error
	ReadChunk(chunkID string) ([]byte, error)
	DeleteChunk(chunkID string) error
}

type ReplicatedChunkService interface {
	WriteChunk(chunkID string, data []byte, replicationFactor int) error
	ReadChunk(chunkID string) ([]byte, error)
	DeleteChunk(chunkID string) error
	GetChunkLocations(chunkID string) ([]string, error)
}
