package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sandlib "github.com/AnishMulay/sandstore/clients/library"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
)

type Sample struct {
	WorkerID           int
	Operation          string
	LatencyNanoseconds int64
	Failed             bool
}

type BenchmarkConfig struct {
	Seeds           []string
	Concurrency     int64
	BlockSizeBytes  int64
	DurationSeconds int64
}

type BenchmarkResult struct {
	Operation      string
	Concurrency    int64
	BlockSizeBytes int64
	P50Ms          float64
	P95Ms          float64
	P99Ms          float64
	P100Ms         float64
	TotalOps       int64
	ErrorCount     int64
}

func openFiles(client *sandlib.SandstoreClient, concurrency int, prefix string, flags int) ([]int, error) {
	fds := make([]int, concurrency)
	for i := 0; i < concurrency; i++ {
		fd, err := client.Open(fmt.Sprintf("/%s_worker_%d", prefix, i), flags)
		if err != nil {
			return nil, err
		}
		fds[i] = fd
	}
	return fds, nil
}

func generateBytes(n int64) []byte {
	return make([]byte, n)
}

func percentileMs(sorted []int64, p float64) float64 {
	n := len(sorted)
	index := int(math.Ceil(p/100.0*float64(n))) - 1
	return float64(sorted[index]) / 1e6
}

func aggregate(results <-chan Sample, operation string, cfg BenchmarkConfig, w *csv.Writer) (BenchmarkResult, error) {
	latencies := make([]int64, 0)
	var errorCount int64
	for sample := range results {
		latencies = append(latencies, sample.LatencyNanoseconds)
		if sample.Failed {
			errorCount++
		}
	}

	sort.Slice(latencies, func(i int, j int) bool {
		return latencies[i] < latencies[j]
	})

	result := BenchmarkResult{
		Operation:      operation,
		Concurrency:    cfg.Concurrency,
		BlockSizeBytes: cfg.BlockSizeBytes,
		TotalOps:       int64(len(latencies)),
		ErrorCount:     errorCount,
	}

	if len(latencies) > 0 {
		result.P50Ms = percentileMs(latencies, 50)
		result.P95Ms = percentileMs(latencies, 95)
		result.P99Ms = percentileMs(latencies, 99)
		result.P100Ms = percentileMs(latencies, 100)
	}

	if err := w.Write([]string{
		result.Operation,
		strconv.FormatInt(result.Concurrency, 10),
		strconv.FormatInt(result.BlockSizeBytes, 10),
		strconv.FormatFloat(result.P50Ms, 'f', -1, 64),
		strconv.FormatFloat(result.P95Ms, 'f', -1, 64),
		strconv.FormatFloat(result.P99Ms, 'f', -1, 64),
		strconv.FormatFloat(result.P100Ms, 'f', -1, 64),
		strconv.FormatInt(result.TotalOps, 10),
		strconv.FormatInt(result.ErrorCount, 10),
	}); err != nil {
		return BenchmarkResult{}, fmt.Errorf("writing benchmark row for %s: %w", operation, err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return BenchmarkResult{}, fmt.Errorf("flushing benchmark row for %s: %w", operation, err)
	}

	return result, nil
}

func printSummary(results []BenchmarkResult, csvPath string) {
	fmt.Printf("\nOperation   | P50 (ms) | P95 (ms) | P99 (ms) | Total Ops | Errors\n")
	fmt.Printf("------------|----------|----------|----------|-----------|--------\n")
	for _, result := range results {
		fmt.Printf(
			"%-11s | %8.2f | %8.2f | %8.2f | %9d | %6d\n",
			result.Operation,
			result.P50Ms,
			result.P95Ms,
			result.P99Ms,
			result.TotalOps,
			result.ErrorCount,
		)
	}
	fmt.Printf("\nFull results written to: %s\n", csvPath)
}

func main() {
	seedsFlag := flag.String("seeds", "", "comma-separated list of seed addresses")
	concurrencyFlag := flag.Int("concurrency", 0, "worker concurrency")
	durationFlag := flag.Int("duration", 60, "benchmark duration in seconds")
	blockSizeFlag := flag.Int("block-size", 4096, "write block size in bytes")
	flag.Parse()

	if *seedsFlag == "" {
		fmt.Fprintln(os.Stderr, "--seeds is required")
		os.Exit(1)
	}
	if *concurrencyFlag == 0 {
		fmt.Fprintln(os.Stderr, "--concurrency is required")
		os.Exit(1)
	}

	cfg := BenchmarkConfig{
		Seeds:           strings.Split(*seedsFlag, ","),
		Concurrency:     int64(*concurrencyFlag),
		BlockSizeBytes:  int64(*blockSizeFlag),
		DurationSeconds: int64(*durationFlag),
	}

	logDir := filepath.Join("run", "bench", "logs")
	ls := locallog.NewLocalDiscLogService(logDir, "bench", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)

	client, err := sandlib.NewSandstoreClient(cfg.Seeds, comm)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if _, err := client.Stat("/"); err != nil {
		fmt.Fprintf(os.Stderr, "pre-flight failed: cluster unreachable: %v\n", err)
		os.Exit(1)
	}

	for i := 0; i < 10; i++ {
		if _, err := client.Stat("/"); err != nil {
			fmt.Fprintf(os.Stderr, "pre-flight failed: cluster unreachable: %v\n", err)
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(filepath.Join("results", "bench"), 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	csvPath := filepath.Join("results", "bench", fmt.Sprintf("%s.csv", time.Now().Format(time.RFC3339)))
	outputFile, err := os.OpenFile(csvPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer outputFile.Close()

	fileInfo, err := outputFile.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	csvWriter := csv.NewWriter(outputFile)
	if fileInfo.Size() == 0 {
		if err := csvWriter.Write([]string{
			"operation",
			"concurrency",
			"block_size_bytes",
			"p50_ms",
			"p95_ms",
			"p99_ms",
			"p100_ms",
			"total_ops",
			"error_count",
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fds, err := openFiles(client, int(cfg.Concurrency), "bench_write", os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	results := make(chan Sample, int(cfg.Concurrency)*1000)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.DurationSeconds)*time.Second)
	defer cancel()

	buf := generateBytes(cfg.BlockSizeBytes)
	summaryResults := make([]BenchmarkResult, 0, 5)

	var wg sync.WaitGroup
	for i := 0; i < int(cfg.Concurrency); i++ {
		wg.Add(1)
		go func(workerID int, fd int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				start := time.Now()
				_, err := client.Write(fd, buf)
				latency := time.Since(start).Nanoseconds()
				select {
				case results <- Sample{
					WorkerID:           workerID,
					Operation:          "write",
					LatencyNanoseconds: latency,
					Failed:             err != nil,
				}:
				case <-ctx.Done():
					return
				}
			}
		}(i, fds[i])
	}

	wg.Wait()
	close(results)

	writeSummary, err := aggregate(results, "write", cfg, csvWriter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summaryResults = append(summaryResults, writeSummary)

	fmt.Println("write benchmark complete")

	readFds, err := openFiles(client, int(cfg.Concurrency), "bench_write", os.O_RDONLY)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	readResults := make(chan Sample, int(cfg.Concurrency)*1000)

	readCtx, readCancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.DurationSeconds)*time.Second,
	)
	defer readCancel()

	var readWg sync.WaitGroup
	for i := 0; i < int(cfg.Concurrency); i++ {
		readWg.Add(1)
		go func(workerID int, fd int) {
			defer readWg.Done()
			for {
				select {
				case <-readCtx.Done():
					return
				default:
				}

				start := time.Now()
				_, err := client.Read(fd, int(cfg.BlockSizeBytes))
				latency := time.Since(start).Nanoseconds()
				failed := err != nil && err != io.EOF
				if err == io.EOF {
					if closeErr := client.Close(fd); closeErr != nil {
						failed = true
					} else {
						fd, err = client.Open(
							fmt.Sprintf("/bench_write_worker_%d", workerID),
							os.O_RDONLY,
						)
						if err != nil {
							failed = true
						}
					}
				}
				select {
				case readResults <- Sample{
					WorkerID:           workerID,
					Operation:          "read",
					LatencyNanoseconds: latency,
					Failed:             failed,
				}:
				case <-readCtx.Done():
					return
				}
				if err == io.EOF && failed {
					return
				}
			}
		}(i, readFds[i])
	}

	readWg.Wait()
	close(readResults)
	readSummary, err := aggregate(readResults, "read", cfg, csvWriter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summaryResults = append(summaryResults, readSummary)
	fmt.Println("read benchmark complete")

	_, err = openFiles(client, int(cfg.Concurrency), "bench_stat", os.O_CREATE|os.O_WRONLY)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	statResults := make(chan Sample, int(cfg.Concurrency)*1000)
	statCtx, statCancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.DurationSeconds)*time.Second,
	)
	defer statCancel()

	var statWg sync.WaitGroup
	for i := 0; i < int(cfg.Concurrency); i++ {
		statWg.Add(1)
		go func(workerID int) {
			defer statWg.Done()
			for {
				select {
				case <-statCtx.Done():
					return
				default:
				}

				start := time.Now()
				_, err := client.Stat(fmt.Sprintf("/bench_stat_worker_%d", workerID))
				latency := time.Since(start).Nanoseconds()
				_ = err
				select {
				case statResults <- Sample{
					WorkerID:           workerID,
					Operation:          "stat",
					LatencyNanoseconds: latency,
					Failed:             err != nil,
				}:
				case <-statCtx.Done():
					return
				}
			}
		}(i)
	}

	statWg.Wait()
	close(statResults)
	statSummary, err := aggregate(statResults, "stat", cfg, csvWriter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summaryResults = append(summaryResults, statSummary)
	fmt.Println("stat benchmark complete")

	createResults := make(chan Sample, int(cfg.Concurrency)*1000)
	createCtx, createCancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.DurationSeconds)*time.Second,
	)
	defer createCancel()

	var createWg sync.WaitGroup
	for i := 0; i < int(cfg.Concurrency); i++ {
		createWg.Add(1)
		go func(workerID int) {
			defer createWg.Done()
			iteration := 0
			for {
				select {
				case <-createCtx.Done():
					return
				default:
				}

				start := time.Now()
				fd, err := client.Open(fmt.Sprintf("/bench_create_worker_%d_%d", workerID, iteration), os.O_CREATE|os.O_WRONLY)
				latency := time.Since(start).Nanoseconds()
				failed := err != nil
				if err == nil {
					if closeErr := client.Close(fd); closeErr != nil {
						failed = true
					}
				}
				iteration++
				select {
				case createResults <- Sample{
					WorkerID:           workerID,
					Operation:          "create",
					LatencyNanoseconds: latency,
					Failed:             failed,
				}:
				case <-createCtx.Done():
					return
				}
			}
		}(i)
	}

	createWg.Wait()
	close(createResults)
	createSummary, err := aggregate(createResults, "create", cfg, csvWriter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summaryResults = append(summaryResults, createSummary)
	fmt.Println("create benchmark complete")

	listResults := make(chan Sample, int(cfg.Concurrency)*1000)
	listCtx, listCancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.DurationSeconds)*time.Second,
	)
	defer listCancel()

	var listWg sync.WaitGroup
	for i := 0; i < int(cfg.Concurrency); i++ {
		listWg.Add(1)
		go func(workerID int) {
			defer listWg.Done()
			for {
				select {
				case <-listCtx.Done():
					return
				default:
				}

				start := time.Now()
				_, err := client.ListDir("/")
				latency := time.Since(start).Nanoseconds()
				select {
				case listResults <- Sample{
					WorkerID:           workerID,
					Operation:          "listdir",
					LatencyNanoseconds: latency,
					Failed:             err != nil,
				}:
				case <-listCtx.Done():
					return
				}
			}
		}(i)
	}

	listWg.Wait()
	close(listResults)
	listSummary, err := aggregate(listResults, "listdir", cfg, csvWriter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summaryResults = append(summaryResults, listSummary)
	fmt.Println("listdir benchmark complete")
	fmt.Println("all benchmarks complete")
	printSummary(summaryResults, csvPath)
}
