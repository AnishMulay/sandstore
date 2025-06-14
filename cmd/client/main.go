package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	var serverAddr string
	var listenAddr string

	flag.StringVar(&serverAddr, "server", "localhost:8080", "Server address")
	flag.StringVar(&listenAddr, "listen", ":8081", "Client listen address")
	flag.Parse()

	// Create a wait group to wait for response
	var wg sync.WaitGroup
	responseReceived := false
	var responseLock sync.Mutex

	comm := communication.NewHTTPCommunicator(listenAddr)
	
	// Custom handler that signals when response is received
	handler := func(msg communication.Message) (*communication.Message, error) {
		defer func() {
			responseLock.Lock()
			if !responseReceived && msg.Type == "pong" {
				responseReceived = true
				wg.Done()
			}
			responseLock.Unlock()
		}()
		
		log.Printf("Received message from %s of type %s", msg.From, msg.Type)
		
		if msg.Type == "pong" {
			log.Printf("Received pong response from server!")
		}
		
		payload := string(msg.Payload)
		log.Printf("Payload: %s", payload)
		
		prettyJSON, _ := json.MarshalIndent(msg, "", "  ")
		fmt.Printf("Full message:\n%s\n", string(prettyJSON))
		
		return nil, nil
	}
	
	err := comm.Start(handler)
	if err != nil {
		log.Fatalf("Failed to start client: %v", err)
	}
	defer comm.Stop()

	log.Printf("Client started, listening on %s", comm.Address())
	log.Printf("Sending ping to server at %s", serverAddr)

	// Add to wait group before sending message
	wg.Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := communication.Message{
		Type:    "ping",
		Payload: []byte("hello"),
	}

	err = comm.Send(ctx, serverAddr, msg)
	if err != nil {
		log.Fatalf("Failed to send message: %v", err)
	}

	log.Printf("Message sent, waiting for response...")
	
	// Wait for response with timeout
	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()
	
	select {
	case <-waitCh:
		log.Printf("Response received and processed")
	case <-time.After(5 * time.Second):
		log.Printf("Timeout waiting for response")
	}
}