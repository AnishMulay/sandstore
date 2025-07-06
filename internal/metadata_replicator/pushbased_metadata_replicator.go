package metadata_replicator

import (
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
	return nil
}
