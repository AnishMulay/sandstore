package internal

import "errors"

var (
	// Chunk operation errors
	ErrChunkStoreFailed  = errors.New("failed to store chunk")
	ErrChunkReadFailed   = errors.New("failed to read chunk")
	ErrChunkDeleteFailed = errors.New("failed to delete chunk")

	// Metadata operation errors
	ErrMetadataGetFailed    = errors.New("failed to get file metadata")
	ErrMetadataCreateFailed = errors.New("failed to create file metadata")
	ErrMetadataDeleteFailed = errors.New("failed to delete file metadata")

	// Replication errors
	ErrChunkReplicationFailed      = errors.New("failed to replicate chunk")
	ErrMetadataReplicationFailed   = errors.New("failed to replicate metadata")
	ErrReplicatedChunkFetchFailed  = errors.New("failed to fetch replicated chunk")
	ErrReplicatedChunkDeleteFailed = errors.New("failed to delete replicated chunk")
)
