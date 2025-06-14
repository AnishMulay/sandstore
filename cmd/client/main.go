package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	var serverAddr string
	var useGRPC bool
	flag.StringVar(&serverAddr, "server", "localhost:8080", "Server address")
	flag.BoolVar(&useGRPC, "grpc", false, "Use gRPC communicator instead of HTTP")
	flag.Parse()

	// Create message
	msg := communication.Message{
		From:    "client",
		Type:    "ping",
		Payload: []byte("hello"),
	}

	if useGRPC {
		// Use gRPC communicator
		comm := communication.NewGRPCCommunicator(":0") // Client doesn't need to listen on a specific port
		
		// Start the communicator with a simple handler
		if err := comm.Start(func(msg communication.Message) (*communication.Response, error) {
			log.Printf("Received response message: Type=%s, Payload=%s", msg.Type, string(msg.Payload))
			return nil, nil
		}); err != nil {
			log.Fatalf("Failed to start gRPC client: %v", err)
		}
		defer comm.Stop()

		// Send message using gRPC
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		log.Printf("Sending gRPC message to %s", serverAddr)
		if err := comm.Send(ctx, serverAddr, msg); err != nil {
			log.Fatalf("Failed to send gRPC message: %v", err)
		}
		
		log.Println("gRPC message sent successfully")
		// Wait a moment to receive any response
		time.Sleep(1 * time.Second)
	} else {
		// Use HTTP as before
		// Marshal message to JSON
		jsonData, err := json.Marshal(msg)
		if err != nil {
			log.Fatalf("Failed to marshal message: %v", err)
		}

		// Create HTTP request
		url := fmt.Sprintf("http://%s/message", serverAddr)
		req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			log.Fatalf("Failed to create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		// Send request
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("Failed to send request: %v", err)
		}
		defer resp.Body.Close()

		// Read response
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("Failed to read response: %v", err)
		}

		// Print response
		fmt.Printf("Response status: %s\n", resp.Status)
		fmt.Printf("Response body: %s\n", string(body))

		// Try to parse as Message if possible
		var respMsg communication.Message
		if err := json.Unmarshal(body, &respMsg); err == nil {
			fmt.Printf("Parsed response: Type=%s, Payload=%s\n", 
				respMsg.Type, string(respMsg.Payload))
		}
	}
}