package posix

import (
	"os"
	"os/signal"
	"syscall"

	// Core Services
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	clusterinmemory "github.com/AnishMulay/sandstore/internal/cluster_service/inmemory"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
	"github.com/AnishMulay/sandstore/internal/server"

	// POSIX Components
	chunkrepl "github.com/AnishMulay/sandstore/internal/posix_chunk_replicator/default_replicator"
	chunkservice "github.com/AnishMulay/sandstore/internal/posix_chunk_service/local_disc"
	fileservice "github.com/AnishMulay/sandstore/internal/posix_file_service/simple"
	metadatarepl "github.com/AnishMulay/sandstore/internal/posix_metadata_replicator/raft_replicator"
	// Note: We imported the in-memory implementation from the location generated previously
	posixmetadataservice "github.com/AnishMulay/sandstore/internal/posix_metadata_service/inmemory"
	posixserver "github.com/AnishMulay/sandstore/internal/posix_server/simple"
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
	server server.Server // Use generic interface or specific PosixServer
}

func (s *singleNodeServer) Run() error {
	if err := s.server.Start(); err != nil {
		return err
	}

	// Wait for termination signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	return s.server.Stop()
}

func Build(opts Options) runnable {
	// 1. Logging
	logDir := opts.DataDir + "/logs"
	ls := locallog.NewLocalDiscLogService(logDir, opts.NodeID, logservice.InfoLevel)

	// 2. Communication
	// Note: We use the GRPC communicator which supports dynamic type registration
	comm := grpccomm.NewGRPCCommunicator(opts.ListenAddr, ls)

	// 3. Cluster Service (The Phonebook)
	// We pre-populate it with seed peers so Raft knows who to contact.
	var nodes []cluster_service.Node
	for _, peerAddr := range opts.SeedPeers {
		// In this simple setup, we assume ID == Address for seeds if ID isn't explicitly mapped
		// or we filter out self. Ideally, seeds format would be "id=addr", but here we just use addrs.
		if peerAddr != opts.ListenAddr {
			nodes = append(nodes, cluster_service.Node{
				ID:      peerAddr, // Using address as ID for simplicity in this config
				Address: peerAddr,
				Healthy: true,
			})
		}
	}
	// Add self so we are in the list?
	nodes = append(nodes, cluster_service.Node{
		ID:      opts.NodeID,
		Address: opts.ListenAddr,
		Healthy: true,
	})
	
	clusterService := clusterinmemory.NewInMemoryClusterService(nodes, ls)

	// 4. Replicators (The Consensus/Network Layer)
	metaRepl := metadatarepl.NewRaftPosixMetadataReplicator(opts.NodeID, clusterService, comm, ls)
	chunkRepl := chunkrepl.NewDefaultPosixChunkReplicator(clusterService, comm, ls)

	// 5. Core Services (The Logic Layer)
	// Metadata
	ms := posixmetadataservice.NewInMemoryPosixMetadataService(metaRepl, ls)
	
	// Chunks
	chunkDir := opts.DataDir + "/chunks/" + opts.NodeID
	cs := chunkservice.NewPosixLocalDiscChunkService(chunkDir, ls, chunkRepl, 3) // Replication factor 3

	// File Service (Orchestrator)
	fs := fileservice.NewSimplePosixFileService(ms, cs, ls)

	// 6. Server (The Gateway)
	srv := posixserver.NewSimplePosixServer(comm, fs, ls, metaRepl, chunkRepl)

	return &singleNodeServer{server: srv}
}