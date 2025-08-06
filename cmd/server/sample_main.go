package main

// import (
// 	"log"
// 	"os"
// 	"os/signal"
// 	"reflect"
// 	"sync"
// 	"syscall"

// 	"github.com/AnishMulay/sandstore/internal/chunk_replicator"
// 	"github.com/AnishMulay/sandstore/internal/chunk_service"
// 	"github.com/AnishMulay/sandstore/internal/cluster_service"
// 	"github.com/AnishMulay/sandstore/internal/communication"
// 	"github.com/AnishMulay/sandstore/internal/file_service"
// 	"github.com/AnishMulay/sandstore/internal/log_service"
// 	"github.com/AnishMulay/sandstore/internal/metadata_replicator"
// 	"github.com/AnishMulay/sandstore/internal/metadata_service"
// 	"github.com/AnishMulay/sandstore/internal/server"
// )

// func createServer(port string, otherNodes []cluster_service.Node) *server.ReplicatedServer {
// 	logDir := "./logs"
// 	nodeID := port[1:] // Use port as node ID
// 	ls := log_service.NewLocalDiscLogService(logDir, nodeID)
// 	ms := metadata_service.NewInMemoryMetadataService(ls)
// 	chunkPath := "./chunks/" + port[1:] // Use the port to create a unique directory for each server
// 	cs := chunk_service.NewLocalDiscChunkService(chunkPath, ls)
// 	chunkSize := int64(8 * 1024 * 1024)
// 	comm := communication.NewGRPCCommunicator(port, ls)
// 	clusterService := cluster_service.NewInMemoryClusterService(otherNodes, ls)
// 	cr := chunk_replicator.NewDefaultChunkReplicator(clusterService, comm, ls)
// 	mr := metadata_replicator.NewPushBasedMetadataReplicator(clusterService, comm, ls)
// 	fs := file_service.NewReplicatedFileService(ms, cs, cr, mr, ls, chunkSize)
// 	srv := server.NewReplicatedServer(comm, fs, cs, ms, ls, clusterService)

// 	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
// 	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
// 	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)
// 	srv.RegisterTypedHandler(communication.MessageTypeStoreChunk, reflect.TypeOf((*communication.StoreChunkRequest)(nil)).Elem(), srv.HandleStoreChunkMessage)
// 	srv.RegisterTypedHandler(communication.MessageTypeReadChunk, reflect.TypeOf((*communication.ReadChunkRequest)(nil)).Elem(), srv.HandleReadChunkMessage)
// 	srv.RegisterTypedHandler(communication.MessageTypeDeleteChunk, reflect.TypeOf((*communication.DeleteChunkRequest)(nil)).Elem(), srv.HandleDeleteChunkMessage)
// 	srv.RegisterTypedHandler(communication.MessageTypeStoreMetadata, reflect.TypeOf((*communication.StoreMetadataRequest)(nil)).Elem(), srv.HandleStoreMetadataMessage)
// 	srv.RegisterTypedHandler(communication.MessageTypeDeleteMetadata, reflect.TypeOf((*communication.DeleteMetadataRequest)(nil)).Elem(), srv.HandleDeleteMetadataMessage)


// 	return srv
// }

// func main() {
// 	servers := []*server.ReplicatedServer{
// 		createServer(":8080", []cluster_service.Node{
// 			{ID: "8081", Address: "localhost:8081", Healthy: true},
// 			{ID: "8082", Address: "localhost:8082", Healthy: true},
// 		}),
// 		createServer(":8081", []cluster_service.Node{
// 			{ID: "8080", Address: "localhost:8080", Healthy: true},
// 			{ID: "8082", Address: "localhost:8082", Healthy: true},
// 		}),
// 		createServer(":8082", []cluster_service.Node{
// 			{ID: "8080", Address: "localhost:8080", Healthy: true},
// 			{ID: "8081", Address: "localhost:8081", Healthy: true},
// 		}),
// 	}

// 	var wg sync.WaitGroup
// 	for i, srv := range servers {
// 		wg.Add(1)
// 		go func(s *server.ReplicatedServer, port int) {
// 			defer wg.Done()
// 			log.Printf("Starting server on :808%d", port)
// 			if err := s.Start(); err != nil {
// 				log.Printf("Failed to start server on :808%d: %v", port, err)
// 			}
// 		}(srv, i)
// 	}

// 	c := make(chan os.Signal, 1)
// 	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
// 	<-c

// 	log.Println("Shutting down servers...")
// 	var shutdownWg sync.WaitGroup
// 	for i, srv := range servers {
// 		shutdownWg.Add(1)
// 		go func(idx int, server *server.ReplicatedServer) {
// 			defer shutdownWg.Done()
// 			if err := server.Stop(); err != nil {
// 				log.Printf("Error stopping server %d: %v", idx, err)
// 			} else {
// 				log.Printf("Server %d stopped successfully", idx)
// 			}
// 		}(i, srv)
// 	}
// 	shutdownWg.Wait()
// 	log.Println("All servers stopped")
// }
