package server

import "errors"

var (
	// Server lifecycle errors
	ErrServerStartFailed = errors.New("failed to start server")
	ErrServerStopFailed  = errors.New("failed to stop server")

	// Message handling errors
	ErrInvalidPayloadType    = errors.New("invalid payload type for message")
	ErrHandlerNotRegistered  = errors.New("no handler registered for message type")

	// File operation errors
	ErrFileStoreFailed  = errors.New("failed to store file")
	ErrFileReadFailed   = errors.New("failed to read file")
	ErrFileDeleteFailed = errors.New("failed to delete file")

	// Chunk operation errors
	ErrChunkStoreFailed  = errors.New("failed to store chunk")
	ErrChunkReadFailed   = errors.New("failed to read chunk")
	ErrChunkDeleteFailed = errors.New("failed to delete chunk")

	// Metadata operation errors
	ErrMetadataStoreFailed = errors.New("failed to store metadata")
	ErrMetadataDeleteFailed = errors.New("failed to delete metadata")
)