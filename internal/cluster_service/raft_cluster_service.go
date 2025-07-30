package cluster_service

import (
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"golang.org/x/exp/rand"
)

type RaftState int

const (
	Leader RaftState = iota
	Follower
	Candidate
)

type RaftClusterService struct {
	nodes         []Node
	id            string
	state         RaftState
	currentTerm   int64
	votedFor      string
	leaderID      string
	electionTimer *time.Timer

	comm communication.Communicator
	ls   log_service.LogService
	mu   sync.Mutex
}

func (r *RaftClusterService) RegisterNode(node Node) error {
	r.ls.Info(log_service.LogEvent{
		Message:  "Registering node",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address, "healthy": node.Healthy},
	})

	if node.ID == "" {
		r.ls.Error(log_service.LogEvent{
			Message:  "Invalid node ID",
			Metadata: map[string]any{"nodeID": node.ID},
		})
		return ErrInvalidNodeID
	}

	if node.Address == "" {
		r.ls.Error(log_service.LogEvent{
			Message:  "Invalid node address",
			Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
		})
		return ErrInvalidNodeAddress
	}

	for _, n := range r.nodes {
		if n.ID == node.ID {
			r.ls.Error(log_service.LogEvent{
				Message:  "Node already exists",
				Metadata: map[string]any{"nodeID": node.ID},
			})
			return ErrNodeAlreadyExists
		}
	}

	r.nodes = append(r.nodes, node)

	r.ls.Info(log_service.LogEvent{
		Message:  "Node registered successfully",
		Metadata: map[string]any{"nodeID": node.ID, "totalNodes": len(r.nodes)},
	})

	return nil
}

func (r *RaftClusterService) DeregisterNode(node Node) error {
	r.ls.Info(log_service.LogEvent{
		Message:  "Deregistering node",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
	})

	for i, n := range r.nodes {
		if n.ID == node.ID {
			r.nodes = append(r.nodes[:i], r.nodes[i+1:]...)
			r.ls.Info(log_service.LogEvent{
				Message:  "Node deregistered successfully",
				Metadata: map[string]any{"nodeID": node.ID, "totalNodes": len(r.nodes)},
			})
			return nil
		}
	}

	r.ls.Error(log_service.LogEvent{
		Message:  "Node not found for deregistration",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
	})

	return ErrNodeNotFound
}

func (r *RaftClusterService) GetHealthyNodes() ([]Node, error) {
	r.ls.Debug(log_service.LogEvent{
		Message:  "Getting healthy nodes",
		Metadata: map[string]any{"totalNodes": len(r.nodes)},
	})

	var healthyNodes []Node
	for _, node := range r.nodes {
		if node.Healthy {
			healthyNodes = append(healthyNodes, node)
		}
	}

	if len(healthyNodes) == 0 {
		r.ls.Warn(log_service.LogEvent{
			Message:  "No healthy nodes available",
			Metadata: map[string]any{"totalNodes": len(r.nodes)},
		})
		return nil, ErrNoHealthyNodes
	}

	r.ls.Debug(log_service.LogEvent{
		Message:  "Healthy nodes retrieved",
		Metadata: map[string]any{"healthyNodes": len(healthyNodes), "totalNodes": len(r.nodes)},
	})

	return healthyNodes, nil
}

func (r *RaftClusterService) resetElectionTimer() {
	if r.electionTimer != nil {
		r.electionTimer.Stop()
	}

	timeout := time.Duration(rand.Intn(150)+150) * time.Millisecond
	r.electionTimer = time.AfterFunc(timeout, r.startElection)
}
