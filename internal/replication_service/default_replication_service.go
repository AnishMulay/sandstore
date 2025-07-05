package replication_service

import (
	"context"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/node_registry"
)

type DefaultReplicationService struct {
	nodeRegistry node_registry.NodeRegistry
	comm         communication.Communicator
}

func (rs *DefaultReplicationService) ReplicateChunk(chunkID string, data []byte, replicationFactor int) ([]chunk_service.ChunkReplica, error) {
	nodes, err := rs.nodeRegistry.GetHealthyNodes()
	if err != nil {
		return nil, err
	}

	if len(nodes) < replicationFactor {
		return nil, ErrInsufficientNodes
	}

	var replicas []chunk_service.ChunkReplica
	targetNodes := nodes[:replicationFactor]

	for _, node := range targetNodes {
		msg := communication.Message{
			From: rs.comm.Address(),
			Type: communication.MessageTypeStoreChunk,
			Payload: communication.StoreChunkRequest{
				ChunkID: chunkID,
				Data:    data,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := rs.comm.Send(ctx, node.Address, msg)
		if err != nil {
			return nil, err
		}

		if resp.Code != communication.CodeOK {
			return nil, ErrReplicationFailed
		} else {
			replicas = append(replicas, chunk_service.ChunkReplica{
				NodeID:    node.ID,
				Address:   node.Address,
				ChunkID:   chunkID,
				CreatedAt: time.Now(), // fix this later, this isn't the right way to handle created at in a distributed system
			})
		}
	}

	return replicas, nil
}
