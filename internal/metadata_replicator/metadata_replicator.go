package metadata_replicator

import "github.com/AnishMulay/sandstore/internal/metadata_service"

type MetadataOperationType int

const (
	CREATE MetadataOperationType = iota
	DELETE
)

type MetadataReplicationOp struct {
	Type     MetadataOperationType
	Metadata metadata_service.FileMetadata
}

type MetadataReplicator interface {
	Replicate(op MetadataReplicationOp) error
}
