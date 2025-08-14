package main

import (
	"log"
	"os"
	"os/signal"
	"reflect"
	"syscall"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/server"
)

func main() {
	port := ":8080"
	nodeID := "basic"
	
	ls := log_service.NewLocalDiscLogService("./logs", nodeID, "INFO")
	ms := metadata_service.NewInMemoryMetadataService(ls)
	cs := chunk_service.NewLocalDiscChunkService("./chunks/"+nodeID, ls)
	comm := communication.NewGRPCCommunicator(port, ls)
	clusterService := cluster_service.NewInMemoryClusterService([]cluster_service.Node{}, ls)
	fs := file_service.NewDefaultFileService(ms, cs, ls, 8*1024*1024)
	
	srv := server.NewDefaultServer(comm, fs, ls, clusterService)
	
	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)
	
	log.Printf("Starting basic server on %s", port)
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()
	
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	
	log.Println("Shutting down server...")
	if err := srv.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	}
	log.Println("Server stopped")
}