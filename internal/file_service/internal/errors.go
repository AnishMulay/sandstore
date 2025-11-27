package internal

import "errors"

var (
	// Logic Errors
	ErrInvalidOffset = errors.New("invalid offset")
	ErrInvalidLength = errors.New("invalid length")
	ErrIsDirectory   = errors.New("operation not permitted on a directory")
	ErrNotDirectory  = errors.New("operation requires a directory")
	ErrFileTooLarge  = errors.New("file too large")

	// Dependency Errors (Wrapping lower-level services)
	ErrMetadataActionFailed = errors.New("metadata service operation failed")
	ErrChunkActionFailed    = errors.New("chunk service operation failed")
)