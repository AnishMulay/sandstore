package cluster_service

import (
	"context"
	"time"
)

// NodeStatus represents the liveness state of a node.
type NodeStatus int

const (
	NodeStatusUnknown NodeStatus = iota
	NodeStatusAlive
	NodeStatusSuspect
	NodeStatusDown
)

func (s NodeStatus) String() string {
	switch s {
	case NodeStatusAlive:
		return "Alive"
	case NodeStatusSuspect:
		return "Suspect"
	case NodeStatusDown:
		return "Down"
	default:
		return "Unknown"
	}
}

// ClusterNode represents the STATIC configuration of a node.
// This exists even if the node is currently offline.
type ClusterNode struct {
	ID       string            `json:"id"`
	Address  string            `json:"address"`
	Role     string            `json:"role"` // e.g., "voter", "observer"
	Metadata map[string]string `json:"metadata,omitempty"`
}

// NodeLiveness represents the EPHEMERAL runtime state of a node.
type NodeLiveness struct {
	NodeID        string     `json:"nodeId"`
	Status        NodeStatus `json:"status"`
	LeaseID       int64      `json:"leaseId"` // Underlying Lease ID
	LastRenewedAt time.Time  `json:"lastRenewedAt"`
}

type SafeNode struct {
	ID       string
	Address  string
	Status   NodeStatus
	Metadata map[string]string
}

type NewClusterService interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	// Registration (Node Side)
	RegisterNode(node ClusterNode) error

	// GetHealthyNodes returns a list of nodes that are currently Alive.
	GetHealthyNodes() ([]SafeNode, error)

	// GetAllNodes returns all nodes in the static configuration, regardless of status.
	GetAllNodes() ([]SafeNode, error)

	// Watch allows components to subscribe to cluster changes.
	Watch(callback func())
}