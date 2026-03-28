package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	cluster "github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metrics"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	EtcdDialTimeout = 5 * time.Second
	EtcdSyncTimeout = 10 * time.Second
	etcdOperationTimeout = 10 * time.Second
	LeaseTTL        = 5 // seconds
	PrefixConfig    = "/sandstore/config/nodes/"
	PrefixLease     = "/sandstore/leases/"
)

type EtcdClusterService struct {
	mu             sync.RWMutex
	client         *clientv3.Client
	endpoints      []string
	ls             log_service.LogService
	metricsService metrics.MetricsService

	// Local identity
	selfNode cluster.ClusterNode
	leaseID  clientv3.LeaseID

	// Local Cache
	configCache map[string]cluster.ClusterNode
	// Map of NodeID -> NodeLiveness (Dynamic State)
	livenessCache map[string]cluster.NodeLiveness

	// Callbacks
	watchCallbacks []func()

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewEtcdClusterService(endpoints []string, ls log_service.LogService, metricsService metrics.MetricsService) *EtcdClusterService {
	ctx, cancel := context.WithCancel(context.Background())
	return &EtcdClusterService{
		endpoints:      endpoints,
		ls:             ls,
		metricsService: metricsService,
		configCache:    make(map[string]cluster.ClusterNode),
		livenessCache:  make(map[string]cluster.NodeLiveness),
		ctx:            ctx,
		cancel:         cancel,
	}
}

func (s *EtcdClusterService) Start(ctx context.Context) error {
	start := time.Now()
	defer func() {
		if s == nil || s.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.EtcdClusterServiceStartLatency, elapsed, metrics.MetricTags{
			Operation: "start",
			Service:   "EtcdClusterService",
		})
	}()

	s.ls.Info(log_service.LogEvent{Message: "Starting EtcdClusterService", Metadata: map[string]any{"endpoints": s.endpoints}})

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   s.endpoints,
		DialTimeout: EtcdDialTimeout,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}
	s.client = cli

	syncCtx, cancel := context.WithTimeout(ctx, EtcdSyncTimeout)
	defer cancel()

	if err := s.syncState(syncCtx); err != nil {
		return err
	}

	s.wg.Add(1)
	go s.watchLoop()

	return nil
}

func (s *EtcdClusterService) Stop(ctx context.Context) error {
	s.ls.Info(log_service.LogEvent{Message: "Stopping EtcdClusterService"})
	s.cancel()

	if s.leaseID != 0 {
		_, err := s.client.Revoke(ctx, s.leaseID)
		if err != nil {
			s.ls.Warn(log_service.LogEvent{Message: "Failed to revoke lease during shutdown", Metadata: map[string]any{"error": err.Error()}})
		}
	}

	s.wg.Wait()
	return s.client.Close()
}

func (s *EtcdClusterService) RegisterNode(ctx context.Context, node cluster.ClusterNode) error {
	start := time.Now()
	defer func() {
		if s == nil || s.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.EtcdClusterServiceRegisterNodeLatency, elapsed, metrics.MetricTags{
			Operation: "register_node",
			Service:   "EtcdClusterService",
		})
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.configCache[node.ID]; !ok {
		s.ls.Warn(log_service.LogEvent{Message: "Node registering but not found in static config", Metadata: map[string]any{"id": node.ID}})
	}

	s.selfNode = node

	opCtx, cancel := context.WithTimeout(ctx, etcdOperationTimeout)
	defer cancel()

	resp, err := s.client.Grant(opCtx, LeaseTTL)
	if err != nil {
		return fmt.Errorf("failed to grant lease: %w", err)
	}
	s.leaseID = resp.ID

	liveness := cluster.NodeLiveness{
		NodeID:        node.ID,
		Status:        cluster.NodeStatusAlive,
		LeaseID:       int64(s.leaseID),
		LastRenewedAt: time.Now(),
	}
	val, err := json.Marshal(liveness)
	if err != nil {
		return fmt.Errorf("marshaling node liveness for etcd: %w", err)
	}

	key := PrefixLease + node.ID
	_, err = s.client.Put(opCtx, key, string(val), clientv3.WithLease(s.leaseID))
	if err != nil {
		return fmt.Errorf("failed to put liveness key: %w", err)
	}

	s.ls.Info(log_service.LogEvent{
		Message:  "Node Registered in Cluster",
		Metadata: map[string]any{"id": node.ID, "leaseID": s.leaseID},
	})

	s.wg.Add(1)
	go s.heartbeatLoop()

	return nil
}

func (s *EtcdClusterService) heartbeatLoop() {
	defer s.wg.Done()

	ch, err := s.client.KeepAlive(s.ctx, s.leaseID)
	if err != nil {
		s.ls.Error(log_service.LogEvent{Message: "Failed to start keepalive channel", Metadata: map[string]any{"error": err.Error()}})
		return
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		case ka, ok := <-ch:
			if !ok {
				s.ls.Error(log_service.LogEvent{Message: "Etcd keepalive channel closed unexpectedly"})
				return
			}
			_ = ka
		}
	}
}

func (s *EtcdClusterService) syncState(ctx context.Context) error {
	start := time.Now()
	defer func() {
		if s == nil || s.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.EtcdClusterServiceSyncStateLatency, elapsed, metrics.MetricTags{
			Operation: "sync_state",
			Service:   "EtcdClusterService",
		})
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	respCfg, err := s.client.Get(ctx, PrefixConfig, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	for _, kv := range respCfg.Kvs {
		var n cluster.ClusterNode
		if err := json.Unmarshal(kv.Value, &n); err == nil {
			s.configCache[n.ID] = n
		}
	}

	respLease, err := s.client.Get(ctx, PrefixLease, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	for _, kv := range respLease.Kvs {
		var l cluster.NodeLiveness
		if err := json.Unmarshal(kv.Value, &l); err == nil {
			s.livenessCache[l.NodeID] = l
		}
	}

	return nil
}

func (s *EtcdClusterService) watchLoop() {
	defer s.wg.Done()

	watchCh := s.client.Watch(s.ctx, "/sandstore/", clientv3.WithPrefix())

	for {
		select {
		case <-s.ctx.Done():
			return
		case resp, ok := <-watchCh:
			if !ok {
				return
			}
			for _, ev := range resp.Events {
				s.handleEvent(ev)
			}
		}
	}
}

func (s *EtcdClusterService) handleEvent(ev *clientv3.Event) {
	s.mu.Lock()
	key := string(ev.Kv.Key)
	isConfig := len(key) > len(PrefixConfig) && key[:len(PrefixConfig)] == PrefixConfig
	isLease := len(key) > len(PrefixLease) && key[:len(PrefixLease)] == PrefixLease

	if isConfig {
		if ev.Type == clientv3.EventTypePut {
			var n cluster.ClusterNode
			if err := json.Unmarshal(ev.Kv.Value, &n); err == nil {
				s.configCache[n.ID] = n
			}
		} else if ev.Type == clientv3.EventTypeDelete {
			id := key[len(PrefixConfig):]
			delete(s.configCache, id)
		}
	} else if isLease {
		id := key[len(PrefixLease):]
		if ev.Type == clientv3.EventTypePut {
			var l cluster.NodeLiveness
			if err := json.Unmarshal(ev.Kv.Value, &l); err == nil {
				s.livenessCache[l.NodeID] = l
			}
		} else if ev.Type == clientv3.EventTypeDelete {
			if entry, ok := s.livenessCache[id]; ok {
				entry.Status = cluster.NodeStatusDown
				s.livenessCache[id] = entry
			}
		}
	}
	s.mu.Unlock()

	s.notifyWatchers()
}

func (s *EtcdClusterService) notifyWatchers() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, cb := range s.watchCallbacks {
		go cb()
	}
}

func (s *EtcdClusterService) Watch(callback func()) {
	start := time.Now()
	defer func() {
		if s == nil || s.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.EtcdClusterServiceWatchLatency, elapsed, metrics.MetricTags{
			Operation: "watch",
			Service:   "EtcdClusterService",
		})
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchCallbacks = append(s.watchCallbacks, callback)
}

func (s *EtcdClusterService) GetHealthyNodes() ([]cluster.Node, error) {
	start := time.Now()
	defer func() {
		if s == nil || s.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.EtcdClusterServiceGetHealthyNodesLatency, elapsed, metrics.MetricTags{
			Operation: "get_healthy_nodes",
			Service:   "EtcdClusterService",
		})
	}()

	s.mu.RLock()
	defer s.mu.RUnlock()

	var nodes []cluster.Node
	for id, cfg := range s.configCache {
		liveness, hasLease := s.livenessCache[id]

		isHealthy := hasLease && liveness.Status == cluster.NodeStatusAlive

		if isHealthy {
			nodes = append(nodes, cluster.Node{
				ID:       cfg.ID,
				Address:  cfg.Address,
				Status:   cluster.NodeStatusAlive,
				Metadata: cfg.Metadata,
			})
		}
	}
	return nodes, nil
}

func (s *EtcdClusterService) GetAllNodes() ([]cluster.Node, error) {
	start := time.Now()
	defer func() {
		if s == nil || s.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.EtcdClusterServiceGetAllNodesLatency, elapsed, metrics.MetricTags{
			Operation: "get_all_nodes",
			Service:   "EtcdClusterService",
		})
	}()

	s.mu.RLock()
	defer s.mu.RUnlock()

	var nodes []cluster.Node
	for id, cfg := range s.configCache {
		status := cluster.NodeStatusDown
		if l, ok := s.livenessCache[id]; ok {
			status = l.Status
		}

		nodes = append(nodes, cluster.Node{
			ID:       cfg.ID,
			Address:  cfg.Address,
			Status:   status,
			Metadata: cfg.Metadata,
		})
	}
	return nodes, nil
}
