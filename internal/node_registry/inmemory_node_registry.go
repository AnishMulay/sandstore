package node_registry

import "github.com/AnishMulay/sandstore/internal/log_service"

type InMemoryNodeRegistry struct {
	nodes []Node
	ls    log_service.LogService
}

func NewInMemoryNodeRegistry(nodes []Node, ls log_service.LogService) *InMemoryNodeRegistry {
	return &InMemoryNodeRegistry{
		nodes: nodes,
		ls:    ls,
	}
}

func (r *InMemoryNodeRegistry) RegisterNode(node Node) error {
	r.ls.Info(log_service.LogEvent{
		Message: "Registering node",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address, "healthy": node.Healthy},
	})
	
	if node.ID == "" {
		r.ls.Error(log_service.LogEvent{
			Message: "Invalid node ID",
			Metadata: map[string]any{"nodeID": node.ID},
		})
		return ErrInvalidNodeID
	}
	
	if node.Address == "" {
		r.ls.Error(log_service.LogEvent{
			Message: "Invalid node address",
			Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
		})
		return ErrInvalidNodeAddress
	}
	
	for _, n := range r.nodes {
		if n.ID == node.ID {
			r.ls.Error(log_service.LogEvent{
				Message: "Node already exists",
				Metadata: map[string]any{"nodeID": node.ID},
			})
			return ErrNodeAlreadyExists
		}
	}
	
	r.nodes = append(r.nodes, node)
	
	r.ls.Info(log_service.LogEvent{
		Message: "Node registered successfully",
		Metadata: map[string]any{"nodeID": node.ID, "totalNodes": len(r.nodes)},
	})
	
	return nil
}

func (r *InMemoryNodeRegistry) DeregisterNode(node Node) error {
	r.ls.Info(log_service.LogEvent{
		Message: "Deregistering node",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
	})
	
	for i, n := range r.nodes {
		if n.ID == node.ID {
			r.nodes = append(r.nodes[:i], r.nodes[i+1:]...)
			r.ls.Info(log_service.LogEvent{
				Message: "Node deregistered successfully",
				Metadata: map[string]any{"nodeID": node.ID, "totalNodes": len(r.nodes)},
			})
			return nil
		}
	}
	
	r.ls.Error(log_service.LogEvent{
		Message: "Node not found for deregistration",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
	})
	
	return ErrNodeNotFound
}

func (r *InMemoryNodeRegistry) GetHealthyNodes() ([]Node, error) {
	r.ls.Debug(log_service.LogEvent{
		Message: "Getting healthy nodes",
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
			Message: "No healthy nodes available",
			Metadata: map[string]any{"totalNodes": len(r.nodes)},
		})
		return nil, ErrNoHealthyNodes
	}
	
	r.ls.Debug(log_service.LogEvent{
		Message: "Healthy nodes retrieved",
		Metadata: map[string]any{"healthyNodes": len(healthyNodes), "totalNodes": len(r.nodes)},
	})
	
	return healthyNodes, nil
}
