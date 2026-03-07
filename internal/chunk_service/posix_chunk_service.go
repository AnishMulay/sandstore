package chunk_service

import "context"

type Chunk struct {
	chunkID string
	size    int64
}

type ChunkService interface {
	PrepareChunk(ctx context.Context, txnID string, chunkID string, data []byte, checksum string) error
	CommitChunk(ctx context.Context, txnID string, chunkID string) error
	AbortChunk(ctx context.Context, txnID string, chunkID string) error
	ReadChunk(ctx context.Context, chunkID string) ([]byte, error)
	DeleteChunkLocal(ctx context.Context, chunkID string) error
}
