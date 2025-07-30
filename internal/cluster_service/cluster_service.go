package cluster_service

type Node struct {
	ID      string
	Address string
	Healthy bool
}

type ClusterService interface {
	RegisterNode(node Node) error
	DeregisterNode(node Node) error
	GetHealthyNodes() ([]Node, error)
}
