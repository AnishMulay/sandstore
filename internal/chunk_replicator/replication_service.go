package chunk_replicator

import (
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
)

type ReplicationService interface {
	ReplicateChunk(chunkID string, data []byte, replicationFactor int) ([]chunk_service.ChunkReplica, error)
	DeleteReplicatedChunk(chunkID string, replicas []chunk_service.ChunkReplica) error
	ReplicateFileMetadata(fileID string, metadata metadata_service.FileMetadata, replicationFactor int) ([]metadata_service.MetadataReplica, error)
}
