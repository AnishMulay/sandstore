package main

import (
	"log"
	"os"
	"os/signal"
	"reflect"
	"syscall"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/server"
)

func main() {
	// Create minimal services
	ms := metadata_service.NewInMemoryMetadataService()
	cs := chunk_service.NewLocalDiscChunkService("./chunks")
	chunkSize := int64(8 * 1024 * 1024) // 8 MB

	fs := file_service.NewDefaultFileService(ms, cs, chunkSize)

	// Create gRPC communicator
	comm := communication.NewGRPCCommunicator(":8080")

	// Create server
	srv := server.NewDefaultServer(comm, fs)

	// Register typed handlers
	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)

	log.Println("Starting server on :8080")
	if err := srv.Start(); err != nil {
		log.Fatal("Failed to start server:", err)
	}

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down server...")
	if err := srv.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	}
}
