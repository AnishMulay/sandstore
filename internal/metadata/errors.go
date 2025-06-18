package metadata

import "errors"

var (
	ErrFileAlreadyExists = errors.New("file already exists")
	ErrFileNotFound      = errors.New("file not found")
)
