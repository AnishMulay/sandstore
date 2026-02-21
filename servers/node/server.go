package node

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	// Core Services
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/server"
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
