package main

import (
	"context"
	"log"

	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
)

func main() {
	ls := locallog.NewLocalDiscLogService("./run/client/logs", "client", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":8083", ls)
	ctx := context.Background()
	serverAddr := "localhost:8101"

	fileData := []byte("Hello, Sandstore! This is a test file for distributed storage. " +
		"This file is intentionally made larger to test the chunking mechanism across the 5-node Raft cluster. " +
		"The system should automatically chunk this file and distribute it across multiple nodes with proper replication.")
	filePath := "test_file.txt"

	storeRequest := communication.StoreFileRequest{
		Path: filePath,
		Data: fileData,
	}

	storeMsg := communication.Message{
		From:    "client",
		Type:    communication.MessageTypeStoreFile,
		Payload: storeRequest,
	}

	log.Printf("Storing file '%s' with %d bytes...", filePath, len(fileData))
	resp, err := comm.Send(ctx, serverAddr, storeMsg)
	if err != nil {
		log.Fatalf("Failed to store file: %v", err)
	}

	log.Printf("File stored successfully! Response code: %s", resp.Code)
	if resp.Code == communication.CodeOK {
		log.Println("✓ File storage test completed successfully")
	} else {
		log.Printf("✗ Store operation failed with code: %s", resp.Code)
	}
}
