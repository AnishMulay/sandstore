package replication_service

import "errors"

var (
	ErrInsufficientNodes = errors.New("insufficient healthy nodes for replication")
	ErrReplicationFailed = errors.New("replication failed on one or more nodes")
)
