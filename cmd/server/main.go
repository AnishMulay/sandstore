package main

import (
	"log"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"

	"github.com/AnishMulay/sandstore/internal/chunk_replicator"
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/server"
)

func createRaftServer(port string, nodeID string, otherNodes []cluster_service.Node) *server.RaftServer {
	logDir := "./logs"
	ls := log_service.NewLocalDiscLogService(logDir, nodeID, "INFO")
	ms := metadata_service.NewInMemoryMetadataService(ls)
	chunkPath := "./chunks/" + nodeID
	cs := chunk_service.NewLocalDiscChunkService(chunkPath, ls)
	chunkSize := int64(8 * 1024 * 1024)
	comm := communication.NewGRPCCommunicator(port, ls)
	raftCluster := cluster_service.NewRaftClusterService(nodeID, otherNodes, comm, ls)
	cr := chunk_replicator.NewDefaultChunkReplicator(raftCluster, comm, ls)
	// mr := metadata_replicator.NewPushBasedMetadataReplicator(raftCluster, comm, ls)
	mr := metadata_replicator.NewRaftMetadataReplicator(raftCluster, ls)
	fs := file_service.NewRaftFileService(ls, mr, cs, ms, cr, chunkSize)
	srv := server.NewRaftServer(comm, fs, cs, ms, ls, raftCluster)

	srv.RegisterTypedHandler(communication.MessageTypeRequestVote, reflect.TypeOf((*communication.RequestVoteRequest)(nil)).Elem(), srv.HandleRequestVoteMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStoreChunk, reflect.TypeOf((*communication.StoreChunkRequest)(nil)).Elem(), srv.HandleStoreChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadChunk, reflect.TypeOf((*communication.ReadChunkRequest)(nil)).Elem(), srv.HandleReadChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteChunk, reflect.TypeOf((*communication.DeleteChunkRequest)(nil)).Elem(), srv.HandleDeleteChunkMessage)
	// srv.RegisterTypedHandler(communication.MessageTypeStoreMetadata, reflect.TypeOf((*communication.StoreMetadataRequest)(nil)).Elem(), srv.HandleStoreMetadataMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStopServer, reflect.TypeOf((*communication.StopServerRequest)(nil)).Elem(), srv.HandleStopServerMessage)
	srv.RegisterTypedHandler(communication.MessageTypeAppendEntries, reflect.TypeOf((*communication.AppendEntriesRequest)(nil)).Elem(), srv.HandleAppendEntriesMessage)

	return srv
}

func main() {
	servers := []*server.RaftServer{
		createRaftServer(":8080", "8080", []cluster_service.Node{
			{ID: "8081", Address: "localhost:8081", Healthy: true},
			{ID: "8082", Address: "localhost:8082", Healthy: true},
		}),
		createRaftServer(":8081", "8081", []cluster_service.Node{
			{ID: "8080", Address: "localhost:8080", Healthy: true},
			{ID: "8082", Address: "localhost:8082", Healthy: true},
		}),
		createRaftServer(":8082", "8082", []cluster_service.Node{
			{ID: "8080", Address: "localhost:8080", Healthy: true},
			{ID: "8081", Address: "localhost:8081", Healthy: true},
		}),
	}

	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(s *server.RaftServer, port int) {
			defer wg.Done()
			log.Printf("Starting Raft server on :808%d", port)
			if err := s.Start(); err != nil {
				log.Printf("Failed to start server on :808%d: %v", port, err)
			}
		}(srv, i)
	}

	log.Println("All Raft servers started - leader election should begin automatically")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down servers...")
	var shutdownWg sync.WaitGroup
	for i, srv := range servers {
		shutdownWg.Add(1)
		go func(idx int, server *server.RaftServer) {
			defer shutdownWg.Done()
			if err := server.Stop(); err != nil {
				log.Printf("Error stopping server %d: %v", idx, err)
			} else {
				log.Printf("Server %d stopped successfully", idx)
			}
		}(i, srv)
	}
	shutdownWg.Wait()
	log.Println("All servers stopped")
}
