package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/server"
)

func main() {
	var useGRPC bool
	var addr string
	flag.BoolVar(&useGRPC, "grpc", false, "Use gRPC communicator instead of HTTP")
	flag.StringVar(&addr, "addr", ":8080", "Address to listen on")
	flag.Parse()

	var comm communication.Communicator
	if useGRPC {
		comm = communication.NewGRPCCommunicator(addr)
		log.Printf("Using gRPC communicator on %s", addr)
	} else {
		comm = communication.NewHTTPCommunicator(addr)
		log.Printf("Using HTTP communicator on %s", addr)
	}

	srv := server.NewServer(comm)

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