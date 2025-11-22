package posix_chunk_replicator

type ChunkReplicator interface {
	ReplicateChunk(chunkID string, data []byte, factor int) error
	FetchReplicatedChunk(chunkID string) ([]byte, error)
	DeleteReplicatedChunk(chunkID string) error
}