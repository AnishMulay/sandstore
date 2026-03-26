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
	simpleserver "github.com/AnishMulay/sandstore/internal/server/simple"
)

func parseEtcdEndpoints(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{"127.0.0.1:2379"}
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
		return []string{"127.0.0.1:2379"}
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
	inner interface{ GetLeaderAddress() string }
}

func (e *externalTopology) GetLeaderAddress() string {
	addr := e.inner.GetLeaderAddress()
	if addr == "" {
		return addr
	}
	hostIP := strings.TrimSpace(os.Getenv("ADVERTISE_HOST"))
	if hostIP == "" {
		hostIP = strings.TrimSpace(os.Getenv("HOST_IP"))
	}
	basePortStr := strings.TrimSpace(os.Getenv("EXTERNAL_BASE_PORT"))
	if hostIP == "" || basePortStr == "" {
		return addr
	}
	basePort, err := strconv.Atoi(basePortStr)
	if err != nil || basePort <= 0 {
		return addr
	}
	// Extract ordinal from NODE_ID suffix, e.g. "sandstore-2" → 2.
	// If the suffix is not a valid integer, ordinal defaults to 0.
	ordinal := 0
	nodeID := strings.TrimSpace(os.Getenv("NODE_ID"))
	if idx := strings.LastIndex(nodeID, "-"); idx >= 0 {
		if n, err2 := strconv.Atoi(nodeID[idx+1:]); err2 == nil && n >= 0 {
			ordinal = n
		}
	}
	return fmt.Sprintf("%s:%d", hostIP, basePort+ordinal)
}

func Build(opts Options) runnable {
	etcdEndpoints := parseEtcdEndpoints(os.Getenv("ETCD_ENDPOINTS"))

	// 1. Logging
	logDir := opts.DataDir + "/logs"
	ls := locallog.NewLocalDiscLogService(logDir, opts.NodeID, logservice.InfoLevel)

	// 2. Metrics
	metricsService := metrics.NewPrometheusMetricsService(":2112", opts.NodeID)
	go metricsService.Start()

	// 2. Communication
	comm := grpccomm.NewGRPCCommunicator(opts.ListenAddr, ls, metricsService)

	// 3. Cluster Service (The Phonebook)
	clusterService := clustercetcd.NewEtcdClusterService(etcdEndpoints, ls, metricsService)
	if err := clusterService.Start(context.Background()); err != nil {
		panic(err)
	}
	if err := clusterService.RegisterNode(cluster_service.ClusterNode{
		ID:      opts.NodeID,
		Address: opts.ListenAddr,
	}); err != nil {
		panic(err)
	}

	ms, err := metadataservice.NewBoltMetadataService(opts.DataDir+"/state.db", metricsService)
	if err != nil {
		panic(err)
	}

	// 4. Replicators (The Consensus/Network Layer)
	raftConfig := durableraft.RaftConfig{
		MaxBatchSize:          envInt("RAFT_MAX_BATCH_SIZE", 64),
		MaxBatchWaitTime:      time.Duration(envInt("RAFT_MAX_BATCH_WAIT_MS", 10)) * time.Millisecond,
		SnapshotThresholdLogs: uint64(envInt("RAFT_SNAPSHOT_THRESHOLD_LOGS", 1000)),
	}
	logStore, err := durableraft.NewFileLogStore(opts.DataDir+"/raft_wal.json", metricsService)
	if err != nil {
		panic(err)
	}
	stableStore, err := durableraft.NewFileStableStore(opts.DataDir+"/raft_stable.json", metricsService)
	if err != nil {
		panic(err)
	}
	snapshotStore := durableraft.NewFileSnapshotStore(opts.DataDir+"/raft_snapshot.bin", metricsService)

	metaRepl := durableraft.NewDurableRaftReplicator(opts.NodeID, clusterService, comm, ls, raftConfig, logStore, stableStore, snapshotStore, metricsService, ms)

	ms.SetReplicator(metaRepl)
	chunkDir := opts.DataDir + "/chunks/" + opts.NodeID

	// cs now implements the 2PC interface (Prepare, Commit, Abort)
	cs := chunkservice.NewLocalDiscChunkService(chunkDir, ls, metricsService)

	chunkSize := int64(8 * 1024 * 1024) // 8MB default
	replicaCount := 3

	placementStrategy := orchestrators.NewLegacySortedPlacementStrategy(clusterService, replicaCount, metricsService)
	endpointResolver := orchestrators.NewStaticEndpointResolver(clusterService)
	dpo := orchestrators.NewRaftDataPlaneOrchestrator(comm, endpointResolver, chunkSize, cs, metricsService)
	txnCoordinator := orchestrators.NewRaftTransactionCoordinator(comm, metaRepl, metricsService)
	cpo := orchestrators.NewControlPlaneOrchestrator(ms, placementStrategy, txnCoordinator, metaRepl, metricsService, chunkSize, replicaCount)

	// 6. Server (The Gateway)
	var topology simpleserver.TopologyProvider = metaRepl
	hostIP := strings.TrimSpace(os.Getenv("ADVERTISE_HOST"))
	if hostIP == "" {
		hostIP = strings.TrimSpace(os.Getenv("HOST_IP"))
	}
	extBasePort := strings.TrimSpace(os.Getenv("EXTERNAL_BASE_PORT"))
	if hostIP != "" && extBasePort != "" {
		topology = &externalTopology{inner: metaRepl}
	}
	srv := simpleserver.NewSimpleServer(comm, cpo, dpo, ls, topology, metricsService)

	return &singleNodeServer{
		server:         srv,
		clusterService: clusterService,
	}
}
