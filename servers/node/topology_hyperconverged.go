package node

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	clustercetcd "github.com/AnishMulay/sandstore/internal/cluster_service/etcd"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"

	chunkservice "github.com/AnishMulay/sandstore/internal/chunk_service/local_disc"
	durableraft "github.com/AnishMulay/sandstore/internal/metadata_replicator/durable_raft"
	metadataservice "github.com/AnishMulay/sandstore/internal/metadata_service/bolt"
	metrics "github.com/AnishMulay/sandstore/internal/metrics/prometheus"
	"github.com/AnishMulay/sandstore/internal/orchestrators"
	hyperconvergedserver "github.com/AnishMulay/sandstore/internal/server/hyperconverged"
)

const (
	// defaultEtcdEndpoint is the local etcd endpoint used when ETCD_ENDPOINTS is unset.
	defaultEtcdEndpoint = "127.0.0.1:2379"

	// defaultMetricsListenAddr is the fixed Prometheus listener for the node process.
	defaultMetricsListenAddr = ":2112"

	// defaultRaftMaxBatchSize is the fallback batch size for Raft replication.
	defaultRaftMaxBatchSize = 64

	// defaultRaftMaxBatchWaitMilliseconds bounds how long Raft batching waits before flushing.
	defaultRaftMaxBatchWaitMilliseconds = 10

	// defaultRaftSnapshotThresholdLogs controls how many Raft log entries accumulate before snapshotting.
	defaultRaftSnapshotThresholdLogs = 1000

	// defaultChunkSizeBytes is the default maximum size of a single chunk.
	// 8 MiB matches the internal write buffer size expected by the node topology.
	defaultChunkSizeBytes = 8 * 1024 * 1024

	// defaultReplicaCount is the number of replicas requested for each chunk.
	defaultReplicaCount = 3

	// defaultClusterRegistrationTimeout bounds how long node registration may block during startup.
	defaultClusterRegistrationTimeout = 30 * time.Second
)

func parseEtcdEndpoints(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{defaultEtcdEndpoint}
	}

	parts := strings.Split(raw, ",")
	endpoints := make([]string, 0, len(parts))
	for _, part := range parts {
		ep := strings.TrimSpace(part)
		if ep != "" {
			endpoints = append(endpoints, ep)
		}
	}
	if len(endpoints) == 0 {
		return []string{defaultEtcdEndpoint}
	}
	return endpoints
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// externalTopology wraps a TopologyProvider and translates the leader address
// from an internal DNS name to ADVERTISE_HOST:EXTERNAL_BASE_PORT+ordinal when
// both env vars are set. When ADVERTISE_HOST is absent it falls back to
// HOST_IP. When either host or port is absent the inner provider's address is
// returned unchanged, preserving existing smoke-test behavior.
type externalTopology struct {
	inner            interface{ GetLeaderAddress() string }
	advertiseHost    string
	externalBasePort string
	nodeID           string
}

func (e *externalTopology) GetLeaderAddress() string {
	addr := e.inner.GetLeaderAddress()
	if addr == "" {
		return addr
	}
	if e.advertiseHost == "" || e.externalBasePort == "" {
		return addr
	}
	basePort, err := strconv.Atoi(e.externalBasePort)
	if err != nil || basePort <= 0 {
		return addr
	}
	// Extract ordinal from NODE_ID suffix, e.g. "sandstore-hyperconverged-2" → 2.
	// If the suffix is not a valid integer, ordinal defaults to 0.
	ordinal := 0
	if idx := strings.LastIndex(e.nodeID, "-"); idx >= 0 {
		if n, err2 := strconv.Atoi(e.nodeID[idx+1:]); err2 == nil && n >= 0 {
			ordinal = n
		}
	}
	return fmt.Sprintf("%s:%d", e.advertiseHost, basePort+ordinal)
}

// hyperconvergedConfig holds all runtime configuration for the hyperconverged
// topology, parsed from startup options and environment variables.
type hyperconvergedConfig struct {
	NodeID                     string
	ListenAddr                 string
	DataDir                    string
	LogDir                     string
	ChunkDir                   string
	EtcdEndpoints              []string
	MetricsListenAddr          string
	AdvertiseHost              string
	ExternalBasePort           string
	RaftConfig                 durableraft.RaftConfig
	ChunkSizeBytes             int64
	ReplicaCount               int
	ClusterRegistrationTimeout time.Duration
}

// buildConfig reads and validates the environment-backed configuration for the
// hyperconverged topology.
func buildConfig(opts Options) (hyperconvergedConfig, error) {
	cfg := hyperconvergedConfig{
		NodeID:            opts.NodeID,
		ListenAddr:        opts.ListenAddr,
		DataDir:           opts.DataDir,
		LogDir:            opts.DataDir + "/logs",
		ChunkDir:          opts.DataDir + "/chunks/" + opts.NodeID,
		EtcdEndpoints:     parseEtcdEndpoints(os.Getenv("ETCD_ENDPOINTS")),
		MetricsListenAddr: defaultMetricsListenAddr,
		RaftConfig: durableraft.RaftConfig{
			MaxBatchSize:          envInt("RAFT_MAX_BATCH_SIZE", defaultRaftMaxBatchSize),
			MaxBatchWaitTime:      time.Duration(envInt("RAFT_MAX_BATCH_WAIT_MS", defaultRaftMaxBatchWaitMilliseconds)) * time.Millisecond,
			SnapshotThresholdLogs: uint64(envInt("RAFT_SNAPSHOT_THRESHOLD_LOGS", defaultRaftSnapshotThresholdLogs)),
		},
		ChunkSizeBytes:             defaultChunkSizeBytes,
		ReplicaCount:               defaultReplicaCount,
		ClusterRegistrationTimeout: defaultClusterRegistrationTimeout,
	}

	cfg.AdvertiseHost = strings.TrimSpace(os.Getenv("ADVERTISE_HOST"))
	if cfg.AdvertiseHost == "" {
		cfg.AdvertiseHost = strings.TrimSpace(os.Getenv("HOST_IP"))
	}
	cfg.ExternalBasePort = strings.TrimSpace(os.Getenv("EXTERNAL_BASE_PORT"))

	if cfg.NodeID == "" {
		return hyperconvergedConfig{}, fmt.Errorf("node ID is required")
	}
	if cfg.ListenAddr == "" {
		return hyperconvergedConfig{}, fmt.Errorf("listen address is required")
	}
	if cfg.DataDir == "" {
		return hyperconvergedConfig{}, fmt.Errorf("data directory is required")
	}

	return cfg, nil
}

func Build(opts Options) (runnable, error) {
	cfg, err := buildConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("building hyperconverged config: %w", err)
	}

	// 1. Logging
	ls := locallog.NewLocalDiscLogService(cfg.LogDir, cfg.NodeID, logservice.InfoLevel)

	// 2. Metrics
	metricsService := metrics.NewPrometheusMetricsService(cfg.MetricsListenAddr, cfg.NodeID)
	go metricsService.Start()

	// 2. Communication
	comm := grpccomm.NewGRPCCommunicator(cfg.ListenAddr, ls, metricsService)

	// 3. Cluster Service (The Phonebook)
	clusterService := clustercetcd.NewEtcdClusterService(cfg.EtcdEndpoints, ls, metricsService)
	if err := clusterService.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("starting cluster service: %w", err)
	}
	regCtx, regCancel := context.WithTimeout(context.Background(), cfg.ClusterRegistrationTimeout)
	defer regCancel()
	if err := clusterService.RegisterNode(regCtx, cluster_service.ClusterNode{
		ID:      cfg.NodeID,
		Address: cfg.ListenAddr,
	}); err != nil {
		return nil, fmt.Errorf("registering node %q: %w", cfg.NodeID, err)
	}

	ms, err := metadataservice.NewBoltMetadataService(cfg.DataDir+"/state.db", metricsService)
	if err != nil {
		return nil, fmt.Errorf("creating metadata service: %w", err)
	}

	// 4. Replicators (The Consensus/Network Layer)
	logStore, err := durableraft.NewFileLogStore(cfg.DataDir+"/raft_wal.json", metricsService)
	if err != nil {
		return nil, fmt.Errorf("creating raft log store: %w", err)
	}
	stableStore, err := durableraft.NewFileStableStore(cfg.DataDir+"/raft_stable.json", metricsService)
	if err != nil {
		return nil, fmt.Errorf("creating raft stable store: %w", err)
	}
	snapshotStore := durableraft.NewFileSnapshotStore(cfg.DataDir+"/raft_snapshot.bin", metricsService)

	metaRepl := durableraft.NewDurableRaftReplicator(cfg.NodeID, clusterService, comm, ls, cfg.RaftConfig, logStore, stableStore, snapshotStore, metricsService, ms)

	ms.SetReplicator(metaRepl)

	// cs now implements the 2PC interface (Prepare, Commit, Abort)
	cs, err := chunkservice.NewLocalDiscChunkService(cfg.ChunkDir, ls, metricsService)
	if err != nil {
		return nil, fmt.Errorf("creating chunk service: %w", err)
	}

	placementStrategy := orchestrators.NewSortedPlacementStrategy(clusterService, cfg.ReplicaCount, metricsService)
	endpointResolver := orchestrators.NewStaticEndpointResolver(clusterService)
	dpo := orchestrators.NewRaftDataPlaneOrchestrator(comm, endpointResolver, cfg.ChunkSizeBytes, cs, metricsService)
	txnCoordinator := orchestrators.NewRaftTransactionCoordinator(comm, metaRepl, ls, metricsService)
	cpo := orchestrators.NewControlPlaneOrchestrator(ms, placementStrategy, txnCoordinator, metaRepl, metricsService, cfg.ChunkSizeBytes, cfg.ReplicaCount)

	// 6. Server (The Gateway)
	var topology hyperconvergedserver.TopologyProvider = metaRepl
	if cfg.AdvertiseHost != "" && cfg.ExternalBasePort != "" {
		topology = &externalTopology{
			inner:            metaRepl,
			advertiseHost:    cfg.AdvertiseHost,
			externalBasePort: cfg.ExternalBasePort,
			nodeID:           cfg.NodeID,
		}
	}
	srv := hyperconvergedserver.NewHyperconvergedServer(comm, cpo, dpo, ls, topology, metricsService)

	return &singleNodeServer{
		server:         srv,
		clusterService: clusterService,
	}, nil
}
