package internal

import "errors"

var (
	ErrInsufficientNodes       = errors.New("insufficient healthy nodes for replication")
	ErrReplicationFailed       = errors.New("replication failed on one or more nodes")
	ErrChunkNotFound           = errors.New("chunk not found in cluster")
	ErrDeletionFailed          = errors.New("failed to delete chunk from cluster")
	ErrCommunicatorSendFailed  = errors.New("failed to send message via communicator")
)