package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/clients/library"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
)

func main() {
	serverAddr := os.Getenv("SANDSTORE_ADDR")
	if serverAddr == "" {
		serverAddr = "127.0.0.1:9001"
	}

	logDir := filepath.Join("run", "smoke", "logs")
	ls := locallog.NewLocalDiscLogService(logDir, "smoke", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)
	client := sandlib.NewSandstoreClient(serverAddr, comm)

	path := fmt.Sprintf("/sandlib-smoke-%d.txt", time.Now().UnixNano())

	fdCreate, err := client.Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(create) failed on %s for %s: %v", serverAddr, path, err)
	}
	log.Printf("PASS: Open(create) returned fd=%d for %s", fdCreate, path)

	dataCreate, err := client.Read(fdCreate, 64)
	if err != nil {
		log.Fatalf("Read(create-fd) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	if len(dataCreate) != 0 {
		log.Fatalf("Read(create-fd) expected empty data, got %d bytes", len(dataCreate))
	}
	log.Printf("PASS: Read(create-fd) returned %d bytes for %s", len(dataCreate), path)

	fdLookup, err := client.Open(path, os.O_RDONLY)
	if err != nil {
		log.Fatalf("Open(lookup) failed on %s for %s: %v", serverAddr, path, err)
	}
	log.Printf("PASS: Open(lookup) returned fd=%d for %s", fdLookup, path)

	dataLookup, err := client.Read(fdLookup, 64)
	if err != nil {
		log.Fatalf("Read(lookup-fd) failed on %s for %s fd=%d: %v", serverAddr, path, fdLookup, err)
	}
	if len(dataLookup) != 0 {
		log.Fatalf("Read(lookup-fd) expected empty data, got %d bytes", len(dataLookup))
	}
	log.Printf("PASS: Read(lookup-fd) returned %d bytes for %s", len(dataLookup), path)

	racePath := fmt.Sprintf("/sandlib-smoke-race-%d.txt", time.Now().UnixNano())
	const workers = 8

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			fd, openErr := client.Open(racePath, os.O_CREATE|os.O_RDWR)
			if openErr != nil {
				errCh <- fmt.Errorf("worker %d: %w", worker, openErr)
				return
			}

			data, readErr := client.Read(fd, 64)
			if readErr != nil {
				errCh <- fmt.Errorf("worker %d: read failed fd=%d: %w", worker, fd, readErr)
				return
			}
			if len(data) != 0 {
				errCh <- fmt.Errorf("worker %d: expected empty read, got %d bytes", worker, len(data))
				return
			}

			log.Printf("PASS: worker=%d Open+Read(race) fd=%d path=%s bytes=%d", worker, fd, racePath, len(data))
		}(i)
	}

	wg.Wait()
	close(errCh)

	for openErr := range errCh {
		log.Fatalf("Open(race) failed: %v", openErr)
	}
	log.Printf("PASS: Open+Read race handling validated on %s with %d workers", racePath, workers)

	log.Printf("Smoke test complete. target=%s", serverAddr)
}
