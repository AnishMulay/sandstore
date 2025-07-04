package node_registry

type Node struct {
	ID      string
	Address string
	Healthy bool
}

type NodeRegistry interface {
	RegisterNode(node Node) error
	DeregisterNode(node Node) error
	GetHealthyNodes() ([]Node, error)
}
