package node_registry

import "errors"

var (
	// Node registration errors
	ErrNodeAlreadyExists = errors.New("node already exists")
	ErrNodeNotFound      = errors.New("node not found")

	// Node validation errors
	ErrInvalidNodeID      = errors.New("invalid node ID")
	ErrInvalidNodeAddress = errors.New("invalid node address")

	// Registry operation errors
	ErrNoHealthyNodes = errors.New("no healthy nodes available")
)