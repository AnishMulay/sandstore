package pushbased

import (
	"context"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	metadata_replicator "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	mrinternal "github.com/AnishMulay/sandstore/internal/metadata_replicator/internal"
)

type PushBasedMetadataReplicator struct {
	clusterService cluster_service.ClusterService
	comm           communication.Communicator
	ls             log_service.LogService
}

func NewPushBasedMetadataReplicator(clusterService cluster_service.ClusterService, comm communication.Communicator, ls log_service.LogService) *PushBasedMetadataReplicator {
	return &PushBasedMetadataReplicator{
		clusterService: clusterService,
		comm:           comm,
		ls:             ls,
	}
}

func (mr *PushBasedMetadataReplicator) Replicate(op metadata_replicator.MetadataReplicationOp) error {
	opType := "create"
	if op.Type == metadata_replicator.DELETE {
		opType = "delete"
	}

	mr.ls.Info(log_service.LogEvent{
		Message:  "Replicating metadata operation",
		Metadata: map[string]any{"path": op.Metadata.Path, "type": opType, "size": op.Metadata.Size, "chunks": len(op.Metadata.Chunks)},
	})

	nodes, err := mr.clusterService.GetHealthyNodes()
	if err != nil {
		mr.ls.Error(log_service.LogEvent{
			Message:  "Failed to get healthy nodes for metadata replication",
			Metadata: map[string]any{"path": op.Metadata.Path, "error": err.Error()},
		})
		return mrinternal.ErrHealthyNodesGetFailed
	}

	mr.ls.Debug(log_service.LogEvent{
		Message:  "Selected nodes for metadata replication",
		Metadata: map[string]any{"path": op.Metadata.Path, "nodes": len(nodes)},
	})

	for _, node := range nodes {
		mr.ls.Debug(log_service.LogEvent{
			Message:  "Replicating metadata operation to node",
			Metadata: map[string]any{"path": op.Metadata.Path, "type": opType, "nodeID": node.ID, "address": node.Address},
		})

		var msg communication.Message
		if op.Type == metadata_replicator.CREATE {
			msg = communication.Message{
				From:    mr.comm.Address(),
				Type:    communication.MessageTypeStoreMetadata,
				Payload: communication.StoreMetadataRequest{Metadata: op.Metadata},
			}
		} else {
			msg = communication.Message{
				From:    mr.comm.Address(),
				Type:    communication.MessageTypeDeleteMetadata,
				Payload: communication.DeleteMetadataRequest{Path: op.Metadata.Path},
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := mr.comm.Send(ctx, node.Address, msg)
		if err != nil {
			mr.ls.Error(log_service.LogEvent{
				Message:  "Failed to send metadata operation to node",
				Metadata: map[string]any{"path": op.Metadata.Path, "type": opType, "nodeID": node.ID, "address": node.Address, "error": err.Error()},
			})
			return mrinternal.ErrMetadataSendFailed
		}

		if resp.Code != communication.CodeOK {
			mr.ls.Error(log_service.LogEvent{
				Message:  "Metadata operation replication failed",
				Metadata: map[string]any{"path": op.Metadata.Path, "type": opType, "nodeID": node.ID, "address": node.Address, "responseCode": resp.Code, "responseBody": string(resp.Body)},
			})
			return mrinternal.ErrMetadataReplicationFailed
		} else {
			mr.ls.Debug(log_service.LogEvent{
				Message:  "Metadata operation replicated successfully to node",
				Metadata: map[string]any{"path": op.Metadata.Path, "type": opType, "nodeID": node.ID, "address": node.Address},
			})
		}
	}

	mr.ls.Info(log_service.LogEvent{
		Message:  "Metadata operation replication completed",
		Metadata: map[string]any{"path": op.Metadata.Path, "type": opType, "nodes": len(nodes)},
	})

	return nil
}
