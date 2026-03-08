package orchestrators

import (
	"context"
	"fmt"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
)

type StaticEndpointResolver struct {
	clusterService cluster_service.ClusterService
}

var _ EndpointResolver = (*StaticEndpointResolver)(nil)

func NewStaticEndpointResolver(clusterService cluster_service.ClusterService) *StaticEndpointResolver {
	return &StaticEndpointResolver{clusterService: clusterService}
}

func (r *StaticEndpointResolver) ResolveEndpoint(ctx context.Context, logicalAlias string) (string, error) {
	_ = ctx

	nodes, err := r.clusterService.GetAllNodes()
	if err != nil || len(nodes) == 0 {
		nodes, err = r.clusterService.GetHealthyNodes()
		if err != nil {
			return "", err
		}
	}

	for _, node := range nodes {
		if node.ID == logicalAlias {
			return node.Address, nil
		}
	}

	return "", fmt.Errorf("node %s not found in cluster view", logicalAlias)
}
