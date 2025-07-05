package main

import (
	"context"
	"log"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	comm := communication.NewGRPCCommunicator(":8081")

	fileData := []byte("This is sample file content that will be chunked and stored in the sandstore system.")

	storeRequest := communication.StoreFileRequest{
		Path: "test_file.txt",
		Data: fileData,
	}

	msg := communication.Message{
		From:    "client",
		Type:    communication.MessageTypeStoreFile,
		Payload: storeRequest,
	}

	ctx := context.Background()
	serverAddr := "localhost:8080"

	log.Printf("Sending store file message with %d bytes", len(fileData))

	resp, err := comm.Send(ctx, serverAddr, msg)
	if err != nil {
		log.Printf("Failed to send store file message: %v", err)
	} else {
		log.Printf("Store file message sent successfully, response code: %s", resp.Code)
	}
}
