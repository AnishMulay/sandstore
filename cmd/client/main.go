package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	comm := communication.NewGRPCCommunicator(":8081")
	ctx := context.Background()
	serverAddr := "localhost:8080"
	scanner := bufio.NewScanner(os.Stdin)

	fileData := []byte("This is sample file content that will be chunked and stored in the sandstore system.")
	filePath := "test_file.txt"

	// Store file
	storeRequest := communication.StoreFileRequest{
		Path: filePath,
		Data: fileData,
	}

	storeMsg := communication.Message{
		From:    "client",
		Type:    communication.MessageTypeStoreFile,
		Payload: storeRequest,
	}

	log.Printf("Storing file with %d bytes...", len(fileData))
	resp, err := comm.Send(ctx, serverAddr, storeMsg)
	if err != nil {
		log.Printf("Failed to store file: %v", err)
		return
	}
	log.Printf("File stored successfully, response code: %s", resp.Code)

	// Wait for user to check replication
	fmt.Println("\nFile has been stored and replicated.")
	fmt.Println("Please check that chunks have been replicated correctly on all nodes.")
	fmt.Print("Press Enter when ready to delete the file...")
	scanner.Scan()

	// Delete file
	deleteRequest := communication.DeleteFileRequest{
		Path: filePath,
	}

	deleteMsg := communication.Message{
		From:    "client",
		Type:    communication.MessageTypeDeleteFile,
		Payload: deleteRequest,
	}

	log.Printf("Deleting file...")
	resp, err = comm.Send(ctx, serverAddr, deleteMsg)
	if err != nil {
		log.Printf("Failed to delete file: %v", err)
		return
	}
	log.Printf("File deleted successfully, response code: %s", resp.Code)

	fmt.Println("\nFile has been deleted.")
	fmt.Println("Please verify that all replicated chunks have been removed from all nodes.")
}
