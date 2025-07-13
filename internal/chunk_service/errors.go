package chunk_service

import "errors"

var (
	// Chunk operation errors
	ErrChunkWriteFailed  = errors.New("failed to write chunk")
	ErrChunkReadFailed   = errors.New("failed to read chunk")
	ErrChunkDeleteFailed = errors.New("failed to delete chunk")
	ErrChunkNotFound     = errors.New("chunk not found")
)