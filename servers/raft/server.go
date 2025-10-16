package raft

import (
	"os"
	"os/signal"
	"reflect"
	"syscall"

	chunkreplicator "github.com/AnishMulay/sandstore/internal/chunk_replicator/defaultreplicator"
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	fileserviceraft "github.com/AnishMulay/sandstore/internal/file_service/raft"
	"github.com/AnishMulay/sandstore/internal/log_service"
	metadataraft "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/server"
)

type Options struct {
	NodeID     string
	ListenAddr string
	DataDir    string
	SeedPeers  []string
	Bootstrap  bool
}

type runnable interface {
	Run() error
}

type singleNodeServer struct {
	server *server.RaftServer
}

func (s *singleNodeServer) Run() error {
	if err := s.server.Start(); err != nil {
		return err
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	return s.server.Stop()
}

func Build(opts Options) runnable {
	var otherNodes []cluster_service.Node
	for _, peer := range opts.SeedPeers {
		if peer != opts.ListenAddr {
			otherNodes = append(otherNodes, cluster_service.Node{
				ID:      peer,
				Address: peer,
				Healthy: true,
			})
		}
	}

	ls := log_service.NewLocalDiscLogService(opts.DataDir+"/logs", opts.NodeID, "INFO")
	ms := metadata_service.NewInMemoryMetadataService(ls)
	cs := chunk_service.NewLocalDiscChunkService(opts.DataDir+"/chunks", ls)
	comm := communication.NewGRPCCommunicator(opts.ListenAddr, ls)
	raftCluster := cluster_service.NewRaftClusterService(opts.NodeID, otherNodes, comm, ls)
	cr := chunkreplicator.NewDefaultChunkReplicator(raftCluster, comm, ls)
	mr := metadataraft.NewRaftMetadataReplicator(raftCluster, ls, ms)
	fs := fileserviceraft.NewRaftFileService(ls, mr, cs, ms, cr, 8*1024*1024)
	srv := server.NewRaftServer(comm, fs, cs, ms, ls, raftCluster)

	srv.RegisterTypedHandler(communication.MessageTypeRequestVote, reflect.TypeOf((*communication.RequestVoteRequest)(nil)).Elem(), srv.HandleRequestVoteMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStoreChunk, reflect.TypeOf((*communication.StoreChunkRequest)(nil)).Elem(), srv.HandleStoreChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadChunk, reflect.TypeOf((*communication.ReadChunkRequest)(nil)).Elem(), srv.HandleReadChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteChunk, reflect.TypeOf((*communication.DeleteChunkRequest)(nil)).Elem(), srv.HandleDeleteChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStopServer, reflect.TypeOf((*communication.StopServerRequest)(nil)).Elem(), srv.HandleStopServerMessage)
	srv.RegisterTypedHandler(communication.MessageTypeAppendEntries, reflect.TypeOf((*communication.AppendEntriesRequest)(nil)).Elem(), srv.HandleAppendEntriesMessage)

	return &singleNodeServer{server: srv}
}
