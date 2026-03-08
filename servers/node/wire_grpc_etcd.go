//go:build grpc && etcd

// This file is only compiled when both 'grpc' and 'etcd' build tags are set.
// It keeps transport/cluster wiring modular at compile time.

package node

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	clustercetcd "github.com/AnishMulay/sandstore/internal/cluster_service/etcd"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"

	chunkrepl "github.com/AnishMulay/sandstore/internal/chunk_replicator/default_replicator"
	chunkservice "github.com/AnishMulay/sandstore/internal/chunk_service/local_disc"
	durableraft "github.com/AnishMulay/sandstore/internal/metadata_replicator/durable_raft"
	metadataservice "github.com/AnishMulay/sandstore/internal/metadata_service/bolt"
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

func Build(opts Options) runnable {
	etcdEndpoints := parseEtcdEndpoints(os.Getenv("ETCD_ENDPOINTS"))

	// 1. Logging
	logDir := opts.DataDir + "/logs"
	ls := locallog.NewLocalDiscLogService(logDir, opts.NodeID, logservice.InfoLevel)

	// 2. Communication
	comm := grpccomm.NewGRPCCommunicator(opts.ListenAddr, ls)

	// 3. Cluster Service (The Phonebook)
	clusterService := clustercetcd.NewEtcdClusterService(etcdEndpoints, ls)
	if err := clusterService.Start(context.Background()); err != nil {
		panic(err)
	}
	if err := clusterService.RegisterNode(cluster_service.ClusterNode{
		ID:      opts.NodeID,
		Address: opts.ListenAddr,
	}); err != nil {
		panic(err)
	}

	// 5. Core Services (The Logic Layer)
	ms, err := metadataservice.NewBoltMetadataService(opts.DataDir + "/state.db")
	if err != nil {
		panic(err)
	}

	// 4. Replicators (The Consensus/Network Layer)
	raftConfig := durableraft.RaftConfig{
		MaxBatchSize:          envInt("RAFT_MAX_BATCH_SIZE", 64),
		MaxBatchWaitTime:      time.Duration(envInt("RAFT_MAX_BATCH_WAIT_MS", 10)) * time.Millisecond,
		SnapshotThresholdLogs: uint64(envInt("RAFT_SNAPSHOT_THRESHOLD_LOGS", 1000)),
	}
	logStore, err := durableraft.NewFileLogStore(opts.DataDir + "/raft_wal.json")
	if err != nil {
		panic(err)
	}
	stableStore := durableraft.NewFileStableStore(opts.DataDir + "/raft_stable.json")
	snapshotStore := durableraft.NewFileSnapshotStore(opts.DataDir + "/raft_snapshot.bin")

	metaRepl := durableraft.NewDurableRaftReplicator(opts.NodeID, clusterService, comm, ls, raftConfig, logStore, stableStore, snapshotStore, ms)
	chunkRepl := chunkrepl.NewDefaultChunkReplicator(clusterService, comm, ls)

	ms.SetReplicator(metaRepl)
	chunkDir := opts.DataDir + "/chunks/" + opts.NodeID

	// cs now implements the 2PC interface (Prepare, Commit, Abort)
	// chunkRepl is no longer used by ChunkService, it's strictly local.
	cs := chunkservice.NewLocalDiscChunkService(chunkDir, ls)

	chunkSize := int64(8 * 1024 * 1024) // 8MB default
	replicaCount := 3

	placementStrategy := orchestrators.NewLegacySortedPlacementStrategy(clusterService, replicaCount)
	endpointResolver := orchestrators.NewStaticEndpointResolver(clusterService)
	dpo := orchestrators.NewRaftDataPlaneOrchestrator(comm, endpointResolver, chunkSize)
	txnCoordinator := orchestrators.NewRaftTransactionCoordinator(comm, metaRepl)
	cpo := orchestrators.NewControlPlaneOrchestrator(ms, placementStrategy, txnCoordinator, chunkSize, replicaCount)

	// 6. Server (The Gateway)
	srv := simpleserver.NewSimpleServer(comm, cpo, dpo, cs, ls, metaRepl, chunkRepl)

	return &singleNodeServer{
		server:         srv,
		clusterService: clusterService,
	}
}
