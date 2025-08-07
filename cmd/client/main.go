package main

import (
	"context"
	"log"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

func main() {
	// Setup
	logDir := "./logs"
	nodeID := "client"
	ls := log_service.NewLocalDiscLogService(logDir, nodeID)
	comm := communication.NewGRPCCommunicator(":8083", ls)
	ctx := context.Background()
	serverAddr := "localhost:8080"

	// File to store
	fileData := []byte("Hello, Raft! This is a test file for log replication.")
	filePath := "test_file.txt"

	// Store file request
	storeRequest := communication.StoreFileRequest{
		Path: filePath,
		Data: fileData,
	}

	storeMsg := communication.Message{
		From:    "client",
		Type:    communication.MessageTypeStoreFile,
		Payload: storeRequest,
	}

	// Send store request
	log.Printf("Storing file '%s' with %d bytes...", filePath, len(fileData))
	resp, err := comm.Send(ctx, serverAddr, storeMsg)
	if err != nil {
		log.Fatalf("Failed to store file: %v", err)
	}

	log.Printf("File stored successfully! Response code: %s", resp.Code)
	if resp.Code == communication.CodeOK {
		log.Println("✓ Raft log replication test completed successfully")
	} else {
		log.Printf("✗ Store operation failed with code: %s", resp.Code)
	}
}