package metadata_replicator

import "github.com/AnishMulay/sandstore/internal/metadata_service"

type MetadataReplicator interface {
	ReplicateMetadata(metadata metadata_service.FileMetadata) error
}
