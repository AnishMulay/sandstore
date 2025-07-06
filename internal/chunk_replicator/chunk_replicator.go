package chunk_replicator

import (
	"github.com/AnishMulay/sandstore/internal/chunk_service"
)

type ChunkReplicator interface {
	ReplicateChunk(chunkID string, data []byte, replicationFactor int) ([]chunk_service.ChunkReplica, error)
	DeleteReplicatedChunk(chunkID string, replicas []chunk_service.ChunkReplica) error
}
