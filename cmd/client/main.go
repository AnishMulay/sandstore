package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	// Create HTTP communicator
	comm := communication.NewHTTPCommunicator(":8081")

	// Store file message with sample bytes
	fileData := []byte("This is sample file content that will be chunked and stored in the sandstore system. It contains enough data to test the chunking mechanism properly.")

	storeRequest := communication.StoreFileRequest{
		Path: "test_file.txt",
		Data: fileData,
	}

	payload, _ := json.Marshal(storeRequest)

	msg := communication.Message{
		From:    "8081",
		Type:    communication.MessageTypeStoreFile,
		Payload: payload,
	}

	ctx := context.Background()
	serverAddr := "localhost:8080"

	log.Printf("Sending store file message with %d bytes", len(fileData))

	if err := comm.Send(ctx, serverAddr, msg); err != nil {
		log.Printf("Failed to send store file message: %v", err)
	} else {
		log.Printf("Store file message sent successfully")
	}

	log.Println("Client finished")
}
