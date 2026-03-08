package orchestrators

import (
	"context"
	"fmt"
	"sort"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/domain"
)

const defaultReplicaCount = 3

type LegacySortedPlacementStrategy struct {
	clusterService cluster_service.ClusterService
	replicaCount   int
}

var _ PlacementStrategy = (*LegacySortedPlacementStrategy)(nil)

func NewLegacySortedPlacementStrategy(clusterService cluster_service.ClusterService, replicaCount int) *LegacySortedPlacementStrategy {
	if replicaCount <= 0 {
		replicaCount = defaultReplicaCount
	}

	return &LegacySortedPlacementStrategy{
		clusterService: clusterService,
		replicaCount:   replicaCount,
	}
}

func (s *LegacySortedPlacementStrategy) SelectTargets(ctx context.Context, chunkID string, replicaCount int) ([]domain.ChunkLocation, error) {
	_ = ctx
	_ = chunkID

	if replicaCount <= 0 {
		replicaCount = s.replicaCount
	}
	if replicaCount <= 0 {
		replicaCount = defaultReplicaCount
	}

	nodes, err := s.clusterService.GetAllNodes()
	if err != nil || len(nodes) == 0 {
		nodes, err = s.clusterService.GetHealthyNodes()
		if err != nil {
			return nil, err
		}
	}

	filtered := make([]cluster_service.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.ID == "" || node.Address == "" {
			continue
		}
		filtered = append(filtered, node)
	}

	if len(filtered) < replicaCount {
		return nil, fmt.Errorf("insufficient nodes for prepare: need %d have %d", replicaCount, len(filtered))
	}

	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })

	targets := make([]domain.ChunkLocation, replicaCount)
	for i, node := range filtered[:replicaCount] {
		targets[i] = domain.ChunkLocation{
			LogicalNodeAlias: node.ID,
			PhysicalEndpoint: node.Address,
		}
	}

	return targets, nil
}
