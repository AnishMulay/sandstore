package node

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	// Core Services
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	clustercetcd "github.com/AnishMulay/sandstore/internal/cluster_service/etcd"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
	"github.com/AnishMulay/sandstore/internal/server"

	// POSIX Components
	chunkrepl "github.com/AnishMulay/sandstore/internal/chunk_replicator/default_replicator"
	chunkservice "github.com/AnishMulay/sandstore/internal/chunk_service/local_disc"
	fileservice "github.com/AnishMulay/sandstore/internal/file_service/simple"
	metadatarepl "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	// Note: We imported the in-memory implementation from the location generated previously
	metadataservice "github.com/AnishMulay/sandstore/internal/metadata_service/inmemory"
	simpleserver "github.com/AnishMulay/sandstore/internal/server/simple"
)

type Options struct {
	NodeID     string
	ListenAddr string
	DataDir    string
	SeedPeers  []string
}

type runnable interface {
	Run() error
}

type singleNodeServer struct {
	server         server.Server // Use generic interface or specific Server
	clusterService cluster_service.ClusterService
}

func (s *singleNodeServer) Run() error {
	if err := s.server.Start(); err != nil {
		_ = s.clusterService.Stop(context.Background())
		return err
	}

	// Wait for termination signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	errServer := s.server.Stop()
	errCluster := s.clusterService.Stop(context.Background())

	if errServer != nil {
		return errServer
	}
	return errCluster
}

func Build(opts Options) runnable {
	etcdEndpointsEnv := os.Getenv("ETCD_ENDPOINTS")
	if etcdEndpointsEnv == "" {
		etcdEndpointsEnv = "127.0.0.1:2379"
	}
	etcdEndpoints := strings.Split(etcdEndpointsEnv, ",")
	for i, endpoint := range etcdEndpoints {
		etcdEndpoints[i] = strings.TrimSpace(endpoint)
	}

	// 1. Logging
	logDir := opts.DataDir + "/logs"
	ls := locallog.NewLocalDiscLogService(logDir, opts.NodeID, logservice.InfoLevel)

	// 2. Communication
	// Note: We use the GRPC communicator which supports dynamic type registration
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

	// 4. Replicators (The Consensus/Network Layer)
	metaRepl := metadatarepl.NewRaftMetadataReplicator(opts.NodeID, clusterService, comm, ls)
	chunkRepl := chunkrepl.NewDefaultChunkReplicator(clusterService, comm, ls)

	// 5. Core Services (The Logic Layer)
	// Metadata
	ms := metadataservice.NewInMemoryMetadataService(metaRepl, ls)

	// Chunks
	chunkDir := opts.DataDir + "/chunks/" + opts.NodeID
	cs := chunkservice.NewLocalDiscChunkService(chunkDir, ls, chunkRepl, 2) // Replication factor 3

	// File Service (Orchestrator)
	fs := fileservice.NewSimpleFileService(ms, cs, ls)

	// 6. Server (The Gateway)
	srv := simpleserver.NewSimpleServer(comm, fs, cs, ls, metaRepl, chunkRepl)

	return &singleNodeServer{
		server:         srv,
		clusterService: clusterService,
	}
}
