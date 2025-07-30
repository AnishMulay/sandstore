package chunk_replicator

import (
	"context"
	"net"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type DefaultChunkReplicator struct {
	clusterService cluster_service.ClusterService
	comm           communication.Communicator
	ls             log_service.LogService
}

func NewDefaultChunkReplicator(clusterService cluster_service.ClusterService, comm communication.Communicator, ls log_service.LogService) *DefaultChunkReplicator {
	return &DefaultChunkReplicator{
		clusterService: clusterService,
		comm:           comm,
		ls:             ls,
	}
}

func (cr *DefaultChunkReplicator) ReplicateChunk(chunkID string, data []byte, replicationFactor int) ([]chunk_service.ChunkReplica, error) {
	cr.ls.Info(log_service.LogEvent{
		Message:  "Replicating chunk",
		Metadata: map[string]any{"chunkID": chunkID, "size": len(data), "replicationFactor": replicationFactor},
	})

	nodes, err := cr.clusterService.GetHealthyNodes()
	if err != nil {
		cr.ls.Error(log_service.LogEvent{
			Message:  "Failed to get healthy nodes",
			Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
		})
		return nil, err
	}

	if len(nodes) < replicationFactor {
		cr.ls.Warn(log_service.LogEvent{
			Message:  "Insufficient nodes for replication",
			Metadata: map[string]any{"chunkID": chunkID, "availableNodes": len(nodes), "required": replicationFactor},
		})
		return nil, ErrInsufficientNodes
	}

	var replicas []chunk_service.ChunkReplica
	targetNodes := nodes[:replicationFactor]

	cr.ls.Debug(log_service.LogEvent{
		Message:  "Selected target nodes for replication",
		Metadata: map[string]any{"chunkID": chunkID, "targetNodes": len(targetNodes)},
	})

	// this is a placeholder for now, future implementation will fix this to get the correct nodeID
	_, port, _ := net.SplitHostPort(cr.comm.Address())

	replicas = append(replicas, chunk_service.ChunkReplica{
		NodeID:    port, // Use the current node's ID
		Address:   cr.comm.Address(),
		ChunkID:   chunkID,
		CreatedAt: time.Now(),
	})

	for _, node := range targetNodes {
		cr.ls.Debug(log_service.LogEvent{
			Message:  "Replicating chunk to node",
			Metadata: map[string]any{"chunkID": chunkID, "nodeID": node.ID, "address": node.Address},
		})

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
			cr.ls.Error(log_service.LogEvent{
				Message:  "Failed to send chunk to node",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": node.ID, "address": node.Address, "error": err.Error()},
			})
			return nil, err
		}

		if resp.Code != communication.CodeOK {
			cr.ls.Error(log_service.LogEvent{
				Message:  "Chunk replication failed",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": node.ID, "address": node.Address, "responseCode": resp.Code},
			})
			return nil, ErrReplicationFailed
		} else {
			cr.ls.Debug(log_service.LogEvent{
				Message:  "Chunk replicated successfully to node",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": node.ID, "address": node.Address},
			})
			replicas = append(replicas, chunk_service.ChunkReplica{
				NodeID:    node.ID,
				Address:   node.Address,
				ChunkID:   chunkID,
				CreatedAt: time.Now(), // fix this later, this isn't the right way to handle created at in a distributed system
			})
		}
	}

	cr.ls.Info(log_service.LogEvent{
		Message:  "Chunk replication completed",
		Metadata: map[string]any{"chunkID": chunkID, "replicas": len(replicas)},
	})

	return replicas, nil
}

func (cr *DefaultChunkReplicator) FetchReplicatedChunk(chunkID string, replicas []chunk_service.ChunkReplica) ([]byte, error) {
	cr.ls.Info(log_service.LogEvent{
		Message:  "Fetching replicated chunk",
		Metadata: map[string]any{"chunkID": chunkID, "replicas": len(replicas)},
	})

	for _, replica := range replicas {
		cr.ls.Debug(log_service.LogEvent{
			Message:  "Attempting to fetch chunk from replica",
			Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address},
		})

		msg := communication.Message{
			From: cr.comm.Address(),
			Type: communication.MessageTypeReadChunk,
			Payload: communication.ReadChunkRequest{
				ChunkID: chunkID,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := cr.comm.Send(ctx, replica.Address, msg)
		if err != nil {
			cr.ls.Warn(log_service.LogEvent{
				Message:  "Failed to fetch chunk from replica",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address, "error": err.Error()},
			})
			continue
		}

		if resp.Code == communication.CodeOK {
			cr.ls.Info(log_service.LogEvent{
				Message:  "Chunk fetched successfully from replica",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address, "size": len(resp.Body)},
			})
			return resp.Body, nil
		} else {
			cr.ls.Warn(log_service.LogEvent{
				Message:  "Failed to fetch chunk from replica",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address, "responseCode": resp.Code},
			})
		}
	}

	cr.ls.Error(log_service.LogEvent{
		Message:  "Replicated chunk not found in any replica",
		Metadata: map[string]any{"chunkID": chunkID, "replicas": len(replicas)},
	})

	return nil, ErrReplicatedChunkNotFound
}

func (cr *DefaultChunkReplicator) DeleteReplicatedChunk(chunkID string, replicas []chunk_service.ChunkReplica) error {
	cr.ls.Info(log_service.LogEvent{
		Message:  "Deleting replicated chunk",
		Metadata: map[string]any{"chunkID": chunkID, "replicas": len(replicas)},
	})

	for _, replica := range replicas {
		cr.ls.Debug(log_service.LogEvent{
			Message:  "Deleting chunk from replica",
			Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address},
		})

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
			cr.ls.Error(log_service.LogEvent{
				Message:  "Failed to delete chunk from replica",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address, "error": err.Error()},
			})
			return err
		}

		if resp.Code != communication.CodeOK {
			cr.ls.Error(log_service.LogEvent{
				Message:  "Failed to delete chunk from replica",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address, "responseCode": resp.Code},
			})
			return ErrDeletionFailed
		} else {
			cr.ls.Debug(log_service.LogEvent{
				Message:  "Chunk deleted successfully from replica",
				Metadata: map[string]any{"chunkID": chunkID, "nodeID": replica.NodeID, "address": replica.Address},
			})
		}
	}

	cr.ls.Info(log_service.LogEvent{
		Message:  "Replicated chunk deletion completed",
		Metadata: map[string]any{"chunkID": chunkID, "replicas": len(replicas)},
	})

	return nil
}
