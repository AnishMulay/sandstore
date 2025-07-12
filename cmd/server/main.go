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
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/node_registry"
	"github.com/AnishMulay/sandstore/internal/server"
)

func createServer(port string, otherNodes []node_registry.Node) *server.ReplicatedServer {
	logDir := "./logs"
	nodeID := port[1:] // Use port as node ID
	ls := log_service.NewLocalDiscLogService(logDir, nodeID)
	ms := metadata_service.NewInMemoryMetadataService(ls)
	chunkPath := "./chunks/" + port[1:] // Use the port to create a unique directory for each server
	cs := chunk_service.NewLocalDiscChunkService(chunkPath, ls)
	chunkSize := int64(8 * 1024 * 1024)
	comm := communication.NewGRPCCommunicator(port)
	nr := node_registry.NewInMemoryNodeRegistry(otherNodes)
	cr := chunk_replicator.NewDefaultChunkReplicator(nr, comm)
	mr := metadata_replicator.NewPushBasedMetadataReplicator(nr, comm)
	fs := file_service.NewReplicatedFileService(ms, cs, cr, mr, ls, chunkSize)
	srv := server.NewReplicatedServer(comm, fs, cs, ms, ls, nr)

	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStoreChunk, reflect.TypeOf((*communication.StoreChunkRequest)(nil)).Elem(), srv.HandleStoreChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadChunk, reflect.TypeOf((*communication.ReadChunkRequest)(nil)).Elem(), srv.HandleReadChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteChunk, reflect.TypeOf((*communication.DeleteChunkRequest)(nil)).Elem(), srv.HandleDeleteChunkMessage)
	srv.RegisterTypedHandler(communication.MessageTypeStoreMetadata, reflect.TypeOf((*communication.StoreMetadataRequest)(nil)).Elem(), srv.HandleStoreMetadataMessage)

	return srv
}

func main() {
	servers := []*server.ReplicatedServer{
		createServer(":8080", []node_registry.Node{
			{ID: "8081", Address: "localhost:8081", Healthy: true},
			{ID: "8082", Address: "localhost:8082", Healthy: true},
		}),
		createServer(":8081", []node_registry.Node{
			{ID: "8080", Address: "localhost:8080", Healthy: true},
			{ID: "8082", Address: "localhost:8082", Healthy: true},
		}),
		createServer(":8082", []node_registry.Node{
			{ID: "8080", Address: "localhost:8080", Healthy: true},
			{ID: "8081", Address: "localhost:8081", Healthy: true},
		}),
	}

	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(s *server.ReplicatedServer, port int) {
			defer wg.Done()
			log.Printf("Starting server on :808%d", port)
			if err := s.Start(); err != nil {
				log.Printf("Failed to start server on :808%d: %v", port, err)
			}
		}(srv, i)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down servers...")
	for i, srv := range servers {
		if err := srv.Stop(); err != nil {
			log.Printf("Error stopping server %d: %v", i, err)
		}
	}
}
