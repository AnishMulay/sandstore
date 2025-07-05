package replication_service

import "errors"

var (
	ErrInsufficientNodes = errors.New("insufficient healthy nodes for replication")
)
