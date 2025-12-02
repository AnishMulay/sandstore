package defaultreplicator

import (
	"context"
	"math/rand"
	"time"

	crepinternal "github.com/AnishMulay/sandstore/internal/chunk_replicator/internal"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type DefaultChunkReplicator struct {
	clusterService cluster_service.ClusterService
	comm           communication.Communicator
	ls             log_service.LogService
}

func NewDefaultChunkReplicator(
	clusterService cluster_service.ClusterService,
	comm communication.Communicator,
	ls log_service.LogService,
) *DefaultChunkReplicator {
	return &DefaultChunkReplicator{
		clusterService: clusterService,
		comm:           comm,
		ls:             ls,
	}
}

func (r *DefaultChunkReplicator) ReplicateChunk(chunkID string, data []byte, factor int) error {
	r.ls.Info(log_service.LogEvent{
		Message:  "Replicating chunk (POSIX)",
		Metadata: map[string]any{"chunkID": chunkID, "factor": factor},
	})

	nodes, err := r.clusterService.GetHealthyNodes()
	if err != nil {
		return crepinternal.ErrInsufficientNodes
	}

	var candidateNodes []cluster_service.Node
	selfAddr := r.comm.Address()
	for _, n := range nodes {
		if n.Address != selfAddr {
			candidateNodes = append(candidateNodes, n)
		}
	}

	//TODO: Handle insufficient nodes more gracefully
	if len(candidateNodes) < factor {
		r.ls.Warn(log_service.LogEvent{
			Message:  "Insufficient nodes for requested replication factor",
			Metadata: map[string]any{"required": factor, "available": len(candidateNodes)},
		})
		if len(candidateNodes) == 0 {
			return crepinternal.ErrInsufficientNodes
		}
	}

	rand.Shuffle(len(candidateNodes), func(i, j int) {
		candidateNodes[i], candidateNodes[j] = candidateNodes[j], candidateNodes[i]
	})

	targets := candidateNodes
	if len(targets) > factor {
		targets = targets[:factor]
	}

	successCount := 0
	for _, node := range targets {
		msg := communication.Message{
			From: r.comm.Address(),
			Type: communication.MessageTypeWriteChunk,
			Payload: communication.WriteChunkRequest{
				ChunkID: chunkID,
				Data:    data,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, err := r.comm.Send(ctx, node.Address, msg)
		cancel()

		if err == nil && resp.Code == communication.CodeOK {
			successCount++
		} else {
			r.ls.Warn(log_service.LogEvent{
				Message:  "Failed to replicate to node",
				Metadata: map[string]any{"node": node.Address, "chunkID": chunkID},
			})
		}
	}

	if successCount == 0 {
		return crepinternal.ErrReplicationFailed
	}

	return nil
}

// FetchReplicatedChunk uses a Scatter-Gather approach to find the chunk
// since we do not store the location metadata in this design.
func (r *DefaultChunkReplicator) FetchReplicatedChunk(chunkID string) ([]byte, error) {
	r.ls.Debug(log_service.LogEvent{
		Message:  "Searching for replicated chunk",
		Metadata: map[string]any{"chunkID": chunkID},
	})

	nodes, err := r.clusterService.GetHealthyNodes()
	if err != nil {
		return nil, err
	}

	// We ask all healthy nodes (except self)
	for _, node := range nodes {
		if node.Address == r.comm.Address() {
			continue
		}

		msg := communication.Message{
			From: r.comm.Address(),
			Type: communication.MessageTypeReadChunk,
			Payload: communication.ReadChunkRequest{
				ChunkID: chunkID,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := r.comm.Send(ctx, node.Address, msg)
		cancel()

		if err == nil && resp.Code == communication.CodeOK {
			r.ls.Info(log_service.LogEvent{
				Message:  "Found chunk replica",
				Metadata: map[string]any{"chunkID": chunkID, "node": node.Address},
			})
			return resp.Body, nil
		}
	}

	return nil, crepinternal.ErrChunkNotFound
}

// DeleteReplicatedChunk broadcasts a delete request to all nodes
// to ensure the chunk is removed wherever it might reside.
func (r *DefaultChunkReplicator) DeleteReplicatedChunk(chunkID string) error {
	r.ls.Info(log_service.LogEvent{
		Message:  "Broadcasting chunk deletion",
		Metadata: map[string]any{"chunkID": chunkID},
	})

	nodes, err := r.clusterService.GetHealthyNodes()
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if node.Address == r.comm.Address() {
			continue
		}

		msg := communication.Message{
			From: r.comm.Address(),
			Type: communication.MessageTypeDeleteChunk,
			Payload: communication.DeleteChunkRequest{
				ChunkID: chunkID,
			},
		}

		// We fire and forget somewhat here, or log errors but don't stop
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := r.comm.Send(ctx, node.Address, msg)
		cancel()

		if err != nil {
			r.ls.Debug(log_service.LogEvent{
				Message:  "Failed to send delete to node",
				Metadata: map[string]any{"node": node.Address, "error": err.Error()},
			})
		}
	}

	return nil
}
