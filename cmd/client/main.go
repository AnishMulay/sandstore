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
	flag.StringVar(&serverAddr, "server", "localhost:8080", "Server address")
	flag.Parse()

	// Create message
	msg := communication.Message{
		From:    "client",
		Type:    "ping",
		Payload: []byte("hello"),
	}

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