package chunk_service

import "time"

type FileChunk struct {
	ChunkID    string
	FileID     string
	Size       int64
	CreatedAt  time.Time
	ModifiedAt time.Time
	Checksum   string
	Replicas   []ChunkReplica
}

type ChunkReplica struct {
	NodeID    string
	Address   string
	ChunkID   string
	CreatedAt time.Time
}

type ChunkService interface {
	WriteChunk(chunkID string, data []byte) error
	ReadChunk(chunkID string) ([]byte, error)
	DeleteChunk(chunkID string) error
}
