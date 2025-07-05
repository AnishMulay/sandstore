package replication_service

import (
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/node_registry"
)

type DefaultReplicationService struct {
	nodeRegistry node_registry.NodeRegistry
	comm         communication.Communicator
}

func (rs *DefaultReplicationService) ReplicateChunk(chunkID string, data []byte, replicationFactor int) ([]chunk_service.ChunkReplica, error) {
	return nil, nil
}
