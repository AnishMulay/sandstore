package metadata_replicator

import "github.com/AnishMulay/sandstore/internal/metadata_service"

type MetadataReplicator interface {
	ReplicateMetadata(fileID string, metadata metadata_service.FileMetadata) ([]metadata_service.MetadataReplica, error)
}
