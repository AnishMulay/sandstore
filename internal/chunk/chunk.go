package chunk

import "time"

type FileChunk struct {
	ChunkID    string
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
