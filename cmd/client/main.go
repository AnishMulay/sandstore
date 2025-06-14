package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

func main() {
	var serverAddr string
	var listenAddr string

	flag.StringVar(&serverAddr, "server", "localhost:8080", "Server address")
	flag.StringVar(&listenAddr, "listen", ":8081", "Client listen address")
	flag.Parse()

	comm := communication.NewHTTPCommunicator(listenAddr)
	
	err := comm.Start(handleMessage)
	if err != nil {
		log.Fatalf("Failed to start client: %v", err)
	}
	defer comm.Stop()

	log.Printf("Client started, listening on %s", comm.Address())
	log.Printf("Sending ping to server at %s", serverAddr)

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
	time.Sleep(2 * time.Second)
}

func handleMessage(msg communication.Message) (*communication.Message, error) {
	log.Printf("Received message from %s of type %s", msg.From, msg.Type)
	
	payload := string(msg.Payload)
	log.Printf("Payload: %s", payload)
	
	prettyJSON, _ := json.MarshalIndent(msg, "", "  ")
	fmt.Printf("Full message:\n%s\n", string(prettyJSON))
	
	return nil, nil
}