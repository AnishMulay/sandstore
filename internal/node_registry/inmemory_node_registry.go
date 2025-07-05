package node_registry

type InMemoryNodeRegistry struct {
	nodes []Node
}

func NewInMemoryNodeRegistry(nodes []Node) *InMemoryNodeRegistry {
	return &InMemoryNodeRegistry{
		nodes: nodes,
	}
}

func (r *InMemoryNodeRegistry) RegisterNode(node Node) error {
	r.nodes = append(r.nodes, node)
	return nil
}

func (r *InMemoryNodeRegistry) DeregisterNode(node Node) error {
	for i, n := range r.nodes {
		if n.ID == node.ID {
			r.nodes = append(r.nodes[:i], r.nodes[i+1:]...)
			return nil
		}
	}
	return nil
}

func (r *InMemoryNodeRegistry) GetHealthyNodes() ([]Node, error) {
	var healthyNodes []Node
	for _, node := range r.nodes {
		if node.Healthy {
			healthyNodes = append(healthyNodes, node)
		}
	}
	return healthyNodes, nil
}
