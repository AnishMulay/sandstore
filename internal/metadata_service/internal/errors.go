package internal

import "errors"

var (
	ErrFileAlreadyExists  = errors.New("file already exists")
	ErrFileNotFound       = errors.New("file not found")
	ErrInvalidPath        = errors.New("invalid file path")
	ErrMissingFileID      = errors.New("file ID is required")
	ErrInvalidSize        = errors.New("file size cannot be negative")
	ErrMissingCreatedAt   = errors.New("created at timestamp is required")
	ErrMissingModifiedAt  = errors.New("modified at timestamp is required")
	ErrMissingPermissions = errors.New("permissions are required")
)
