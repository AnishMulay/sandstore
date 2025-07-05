package replication_service

import "github.com/AnishMulay/sandstore/internal/chunk_service"

type ReplicationService interface {
	ReplicateChunk(chunkID string, data []byte, replicationFactor int) ([]chunk_service.ChunkReplica, error)
}
