package metadata_replicator

import "errors"

var (
	// Node registry errors
	ErrHealthyNodesGetFailed = errors.New("failed to get healthy nodes for replication")

	// Replication errors
	ErrMetadataReplicationFailed = errors.New("failed to replicate metadata to node")
	ErrMetadataSendFailed        = errors.New("failed to send metadata to node")
)