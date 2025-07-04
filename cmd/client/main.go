package main

import (
	"context"
	"log"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	// Create gRPC communicator
	comm := communication.NewGRPCCommunicator(":8081")

	// Store file message with sample bytes
	fileData := []byte("This is sample file content that will be chunked and stored in the sandstore system. It contains enough data to test the chunking mechanism properly.")

	storeRequest := communication.StoreFileRequest{
		Path: "test_file.txt",
		Data: fileData,
	}

	msg := communication.Message{
		From:    "8081",
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

	readFileMsg := communication.Message{
		From:    "8081",
		Type:    communication.MessageTypeReadFile,
		Payload: communication.ReadFileRequest{Path: "test_file.txt"},
	}

	log.Printf("Sending read file message for path: %s", "test_file.txt")

	resp, err = comm.Send(ctx, serverAddr, readFileMsg)
	if err != nil {
		log.Printf("Failed to send read file message: %v", err)
	} else {
		log.Printf("Read file message sent successfully, response code: %s", resp.Code)
		if resp.Code == communication.CodeOK && resp.Body != nil {
			log.Printf("Received file content: %s", string(resp.Body))
		}
	}

	deleteFileMsg := communication.Message{
		From:    "8081",
		Type:    communication.MessageTypeDeleteFile,
		Payload: communication.DeleteFileRequest{Path: "test_file.txt"},
	}

	log.Printf("Sending delete file message for path: %s", "test_file.txt")
	_, err = comm.Send(ctx, serverAddr, deleteFileMsg)

	if err != nil {
		log.Printf("Failed to send delete file message: %v", err)
	}

	log.Printf("Delete file message sent successfully, response code: %s", resp.Code)
	if err != nil {
		log.Printf("Error occurred: %v", err)
	} else {
		log.Printf("File deleted successfully")
	}

	//check if the file is deleted by trying to get the file

	readFileMsg = communication.Message{
		From:    "8081",
		Type:    communication.MessageTypeReadFile,
		Payload: communication.ReadFileRequest{Path: "test_file.txt"},
	}

	log.Printf("Sending read file message for path: %s", "test_file.txt")

	resp, err = comm.Send(ctx, serverAddr, readFileMsg)
	if err != nil {
		log.Printf("Failed to send read file message: %v", err)
	}

	if resp.Code == communication.CodeNotFound {
		log.Printf("File not found, it has been successfully deleted.")
	} else {
		log.Printf("Read file message sent successfully, response code: %s", resp.Code)
		if resp.Code == communication.CodeOK && resp.Body != nil {
			log.Printf("Received file content: %s", string(resp.Body))
		}
	}

	log.Printf("All operations completed successfully")
}
