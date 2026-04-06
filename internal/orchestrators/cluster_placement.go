package orchestrators

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/metrics"
	"github.com/AnishMulay/sandstore/topology/contract"
)

const defaultReplicaCount = 3

type SortedPlacementStrategy struct {
	clusterService cluster_service.ClusterService
	replicaCount   int
	metricsService metrics.MetricsService
}

func NewSortedPlacementStrategy(clusterService cluster_service.ClusterService, replicaCount int, metricsService metrics.MetricsService) *SortedPlacementStrategy {
	if replicaCount <= 0 {
		replicaCount = defaultReplicaCount
	}

	return &SortedPlacementStrategy{
		clusterService: clusterService,
		replicaCount:   replicaCount,
		metricsService: metricsService,
	}
}

func (s *SortedPlacementStrategy) SelectTargets(ctx context.Context, chunkID string, replicaCount int) ([]contract.ChunkLocation, error) {
	start := time.Now()
	defer func() {
		if s == nil || s.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.PlacementStrategySelectTargetsLatency, elapsed, metrics.MetricTags{
			Operation: "select_targets",
			Service:   "SortedPlacementStrategy",
		})
	}()

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

	targets := make([]contract.ChunkLocation, replicaCount)
	for i, node := range filtered[:replicaCount] {
		targets[i] = contract.ChunkLocation{
			LogicalNodeAlias: node.ID,
			PhysicalEndpoint: node.Address,
		}
	}

	return targets, nil
}
