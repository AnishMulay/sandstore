package chunk_service

type Chunk struct {
	chunkID string
	size    int64
}

type ChunkService interface {
	WriteChunk(chunkID string, data []byte) error
	ReadChunk(chunkID string) ([]byte, error)
	DeleteChunk(chunkID string) error
	WriteChunkLocal(chunkID string, data []byte) error
	ReadChunkLocal(chunkID string) ([]byte, error)
	DeleteChunkLocal(chunkID string) error
}
