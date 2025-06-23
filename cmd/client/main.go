package main

import (
	"context"
	"log"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	// Create HTTP communicator
	comm := communication.NewHTTPCommunicator(":8081")
	
	// Test messages
	messages := []communication.Message{
		{
			Type:    "test",
			Payload: []byte("Hello from client!"),
		},
		{
			Type:    "ping",
			Payload: []byte("ping message"),
		},
	}
	
	ctx := context.Background()
	serverAddr := "localhost:8080"
	
	for i, msg := range messages {
		log.Printf("Sending message %d: %s", i+1, msg.Type)
		
		if err := comm.Send(ctx, serverAddr, msg); err != nil {
			log.Printf("Failed to send message %d: %v", i+1, err)
		} else {
			log.Printf("Message %d sent successfully", i+1)
		}
		
		time.Sleep(1 * time.Second)
	}
	
	log.Println("Client finished")
}