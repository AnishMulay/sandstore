package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

func main() {
	logDir := "./logs"
	nodeID := "client"
	ls := log_service.NewLocalDiscLogService(logDir, nodeID)
	comm := communication.NewGRPCCommunicator(":8081", ls)
	ctx := context.Background()
	serverAddr := "localhost:8080"
	readServerAddr := "localhost:8081" // Server to test lazy loading from
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

	// Wait for manual chunk deletion
	fmt.Println("\nFile has been stored and replicated.")
	fmt.Printf("Now manually delete a chunk from server 8081 (localhost:8081)\n")
	fmt.Print("Press Enter after deleting a chunk to test lazy loading...")
	scanner.Scan()

	// Read file to test lazy loading
	readRequest := communication.ReadFileRequest{
		Path: filePath,
	}

	readMsg := communication.Message{
		From:    "client",
		Type:    communication.MessageTypeReadFile,
		Payload: readRequest,
	}

	log.Printf("Reading file to test lazy chunk loading...")
	resp, err = comm.Send(ctx, readServerAddr, readMsg)
	if err != nil {
		log.Printf("Failed to read file: %v", err)
		return
	}
	log.Printf("File read successfully, response code: %s", resp.Code)

	// Print file contents to verify correctness
	fmt.Printf("\nFile contents: %s\n", string(resp.Body))
	fmt.Printf("Original data: %s\n", string(fileData))
	if string(resp.Body) == string(fileData) {
		fmt.Println("✓ File contents match - lazy loading successful!")
	} else {
		fmt.Println("✗ File contents don't match - lazy loading failed!")
	}

	fmt.Print("\nPress Enter to delete the file...")
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
