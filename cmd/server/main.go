package main

import (
	"log"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/node_registry"
	"github.com/AnishMulay/sandstore/internal/replication_service"
	"github.com/AnishMulay/sandstore/internal/server"
)

func createServer(port string, otherNodes []node_registry.Node) *server.DefaultServer {
	ms := metadata_service.NewInMemoryMetadataService()
	cs := chunk_service.NewLocalDiscChunkService("./chunks")
	chunkSize := int64(8 * 1024 * 1024)
	comm := communication.NewGRPCCommunicator(port)
	nr := node_registry.NewInMemoryNodeRegistry(otherNodes)
	rs := replication_service.NewDefaultReplicationService(nr, comm)
	fs := file_service.NewReplicatedFileService(ms, cs, rs, chunkSize)
	srv := server.NewDefaultServer(comm, fs, nr)

	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)

	return srv
}

func main() {
	servers := []*server.DefaultServer{
		createServer(":8080", []node_registry.Node{
			{ID: "node2", Address: "localhost:8081", Healthy: true},
			{ID: "node3", Address: "localhost:8082", Healthy: true},
		}),
		createServer(":8081", []node_registry.Node{
			{ID: "node1", Address: "localhost:8080", Healthy: true},
			{ID: "node3", Address: "localhost:8082", Healthy: true},
		}),
		createServer(":8082", []node_registry.Node{
			{ID: "node1", Address: "localhost:8080", Healthy: true},
			{ID: "node2", Address: "localhost:8081", Healthy: true},
		}),
	}

	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(s *server.DefaultServer, port int) {
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
