package chunk_replicator

import (
	"context"
	"net"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/node_registry"
)

type DefaultChunkReplicator struct {
	nodeRegistry node_registry.NodeRegistry
	comm         communication.Communicator
}

func NewDefaultChunkReplicator(nr node_registry.NodeRegistry, comm communication.Communicator) *DefaultChunkReplicator {
	return &DefaultChunkReplicator{
		nodeRegistry: nr,
		comm:         comm,
	}
}

func (cr *DefaultChunkReplicator) ReplicateChunk(chunkID string, data []byte, replicationFactor int) ([]chunk_service.ChunkReplica, error) {
	nodes, err := cr.nodeRegistry.GetHealthyNodes()
	if err != nil {
		return nil, err
	}

	if len(nodes) < replicationFactor {
		return nil, ErrInsufficientNodes
	}

	var replicas []chunk_service.ChunkReplica
	targetNodes := nodes[:replicationFactor]

	// this is a placeholder for now, future implementation will fix this to get the correct nodeID
	_, port, _ := net.SplitHostPort(cr.comm.Address())

	replicas = append(replicas, chunk_service.ChunkReplica{
		NodeID:    port, // Use the current node's ID
		Address:   cr.comm.Address(),
		ChunkID:   chunkID,
		CreatedAt: time.Now(),
	})

	for _, node := range targetNodes {
		msg := communication.Message{
			From: cr.comm.Address(),
			Type: communication.MessageTypeStoreChunk,
			Payload: communication.StoreChunkRequest{
				ChunkID: chunkID,
				Data:    data,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := cr.comm.Send(ctx, node.Address, msg)
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

func (cr *DefaultChunkReplicator) DeleteReplicatedChunk(chunkID string, replicas []chunk_service.ChunkReplica) error {
	for _, replica := range replicas {
		msg := communication.Message{
			From: cr.comm.Address(),
			Type: communication.MessageTypeDeleteChunk,
			Payload: communication.DeleteChunkRequest{
				ChunkID: chunkID,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := cr.comm.Send(ctx, replica.Address, msg)
		if err != nil {
			return err
		}

		if resp.Code != communication.CodeOK {
			return ErrDeletionFailed
		}
	}

	return nil
}
