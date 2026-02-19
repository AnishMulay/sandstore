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

	logDir := filepath.Join("run", "open-smoke", "logs")
	ls := locallog.NewLocalDiscLogService(logDir, "open-smoke", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)
	client := sandlib.NewSandstoreClient(serverAddr, comm)

	path := fmt.Sprintf("/sandlib-open-smoke-%d.txt", time.Now().UnixNano())

	fdCreate, err := client.Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(create) failed on %s for %s: %v", serverAddr, path, err)
	}
	log.Printf("PASS: Open(create) returned fd=%d for %s", fdCreate, path)

	fdLookup, err := client.Open(path, os.O_RDONLY)
	if err != nil {
		log.Fatalf("Open(lookup) failed on %s for %s: %v", serverAddr, path, err)
	}
	log.Printf("PASS: Open(lookup) returned fd=%d for %s", fdLookup, path)

	racePath := fmt.Sprintf("/sandlib-open-race-%d.txt", time.Now().UnixNano())
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
			log.Printf("PASS: worker=%d Open(race) fd=%d path=%s", worker, fd, racePath)
		}(i)
	}

	wg.Wait()
	close(errCh)

	for openErr := range errCh {
		log.Fatalf("Open(race) failed: %v", openErr)
	}
	log.Printf("PASS: Open race handling validated on %s with %d workers", racePath, workers)

	log.Printf("Smoke test complete. target=%s", serverAddr)
}
