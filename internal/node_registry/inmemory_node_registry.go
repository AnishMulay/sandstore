package node_registry

type InMemoryNodeRegistry struct {
	nodes []Node
}

func NewInMemoryNodeRegistry() *InMemoryNodeRegistry {
	return &InMemoryNodeRegistry{
		nodes: []Node{},
	}
}
