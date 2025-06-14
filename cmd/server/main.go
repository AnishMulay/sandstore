package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/server"
)

func main() {
	comm := communication.NewHTTPCommunicator(":8080")
	srv := server.NewServer(comm)

	srv.RegisterHandler("ping", handlePing)

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	if err := srv.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	}
}

func handlePing(msg communication.Message) (*communication.Message, error) {
	log.Printf("Received ping from %s", msg.From)

	response := &communication.Message{
		Type:    "pong",
		Payload: []byte("pong"),
	}
	return response, nil
}
