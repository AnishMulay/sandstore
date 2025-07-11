package metadata_replicator

import (
	"context"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/node_registry"
)

type PushBasedMetadataReplicator struct {
	nodeRegistry node_registry.NodeRegistry
	comm         communication.Communicator
}

func NewPushBasedMetadataReplicator(nr node_registry.NodeRegistry, comm communication.Communicator) *PushBasedMetadataReplicator {
	return &PushBasedMetadataReplicator{
		nodeRegistry: nr,
		comm:         comm,
	}
}

func (mr *PushBasedMetadataReplicator) ReplicateMetadata(metadata metadata_service.FileMetadata) error {
	nodes, err := mr.nodeRegistry.GetHealthyNodes()
	if err != nil {
		return err
	}

	for _, node := range nodes {
		msg := communication.Message{
			From:    mr.comm.Address(),
			Type:    communication.MessageTypeStoreMetadata,
			Payload: communication.StoreMetadataRequest{Metadata: metadata},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := mr.comm.Send(ctx, node.Address, msg)
		if err != nil {
			return err
		}

		if resp.Code != communication.CodeOK {
			return fmt.Errorf("failed to replicate metadata to node %s: %s", node.ID, resp.Body)
		}
	}

	return nil
}
