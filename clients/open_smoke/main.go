package main

import (
	"bytes"
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

const maxBufferSize = 2 * 1024 * 1024

func main() {
	serverAddr := os.Getenv("SANDSTORE_ADDR")
	if serverAddr == "" {
		serverAddr = "127.0.0.1:9001"
	}

	logDir := filepath.Join("run", "smoke", "logs")
	ls := locallog.NewLocalDiscLogService(logDir, "smoke", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)
	client := sandlib.NewSandstoreClient(serverAddr, comm)

	path := fmt.Sprintf("/sandlib-smoke-open-read-write-%d.txt", time.Now().UnixNano())

	fdCreate, err := client.Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(create) failed on %s for %s: %v", serverAddr, path, err)
	}
	log.Printf("PASS: Open(create) returned fd=%d for %s", fdCreate, path)

	dataCreate, err := readFromFreshFD(client, path, 64)
	if err != nil {
		log.Fatalf("Read(initial) failed on %s for %s: %v", serverAddr, path, err)
	}
	if len(dataCreate) != 0 {
		log.Fatalf("Read(initial) expected empty data, got %d bytes", len(dataCreate))
	}
	log.Printf("PASS: Read(initial) returned %d bytes for %s", len(dataCreate), path)

	fdLookup, err := client.Open(path, os.O_RDWR)
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

	chunkA := makePatternChunk(1*1024*1024, "chunk-a")
	chunkB := makePatternChunk(1536*1024, "chunk-b")
	chunkC := makePatternChunk(1536*1024, "chunk-c")

	writtenA, err := client.Write(fdCreate, chunkA)
	if err != nil {
		log.Fatalf("Write(chunkA) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	if writtenA != len(chunkA) {
		log.Fatalf("Write(chunkA) expected %d bytes, got %d", len(chunkA), writtenA)
	}
	log.Printf("PASS: Write(chunkA) wrote %d bytes", writtenA)

	dataAfterA, err := readFromFreshFD(client, path, len(chunkA)+128)
	if err != nil {
		log.Fatalf("Read(after chunkA) failed on %s for %s: %v", serverAddr, path, err)
	}
	if len(dataAfterA) != 0 {
		log.Fatalf("Read(after chunkA) expected 0 persisted bytes, got %d", len(dataAfterA))
	}
	log.Printf("PASS: Buffered Write(chunkA) not visible before flush")

	writtenB, err := client.Write(fdCreate, chunkB)
	if err != nil {
		log.Fatalf("Write(chunkB) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	if writtenB != len(chunkB) {
		log.Fatalf("Write(chunkB) expected %d bytes, got %d", len(chunkB), writtenB)
	}
	log.Printf("PASS: Write(chunkB) wrote %d bytes (triggered flush of chunkA)", writtenB)

	dataAfterB, err := readFromFreshFD(client, path, len(chunkA)+len(chunkB)+128)
	if err != nil {
		log.Fatalf("Read(after chunkB) failed on %s for %s: %v", serverAddr, path, err)
	}
	if !bytes.Equal(dataAfterB, chunkA) {
		log.Fatalf("Read(after chunkB) expected exactly chunkA (%d bytes), got %d bytes", len(chunkA), len(dataAfterB))
	}
	log.Printf("PASS: Flush-on-overflow persisted chunkA with correct ordering/offset")

	writtenC, err := client.Write(fdCreate, chunkC)
	if err != nil {
		log.Fatalf("Write(chunkC) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	if writtenC != len(chunkC) {
		log.Fatalf("Write(chunkC) expected %d bytes, got %d", len(chunkC), writtenC)
	}
	log.Printf("PASS: Write(chunkC) wrote %d bytes (triggered flush of chunkB)", writtenC)

	expectedAfterC := append(append(make([]byte, 0, len(chunkA)+len(chunkB)), chunkA...), chunkB...)
	dataAfterC, err := readFromFreshFD(client, path, len(expectedAfterC)+len(chunkC)+128)
	if err != nil {
		log.Fatalf("Read(after chunkC) failed on %s for %s: %v", serverAddr, path, err)
	}
	if !bytes.Equal(dataAfterC, expectedAfterC) {
		log.Fatalf("Read(after chunkC) expected chunkA+chunkB (%d bytes), got %d bytes", len(expectedAfterC), len(dataAfterC))
	}
	log.Printf("PASS: Sequential flushes persisted chunkA+chunkB in exact order")

	if len(chunkA)+len(chunkB) <= maxBufferSize {
		log.Fatalf("internal smoke test invariant failed: chunkA+chunkB must exceed maxBufferSize")
	}
	if len(chunkB)+len(chunkC) <= maxBufferSize {
		log.Fatalf("internal smoke test invariant failed: chunkB+chunkC must exceed maxBufferSize")
	}

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

func readFromFreshFD(client *sandlib.SandstoreClient, path string, n int) ([]byte, error) {
	fd, err := client.Open(path, os.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("open for read failed: %w", err)
	}

	data, err := client.Read(fd, n)
	if err != nil {
		return nil, fmt.Errorf("read failed for fd=%d: %w", fd, err)
	}
	return data, nil
}

func makePatternChunk(size int, token string) []byte {
	chunk := make([]byte, size)
	pattern := []byte(token)
	for i := 0; i < size; i++ {
		chunk[i] = pattern[i%len(pattern)]
	}
	return chunk
}
