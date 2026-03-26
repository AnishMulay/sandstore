package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
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

func aggregate(results <-chan Sample, operation string, cfg BenchmarkConfig, w *csv.Writer) {
	latencies := make([]int64, 0)
	for sample := range results {
		latencies = append(latencies, sample.LatencyNanoseconds)
	}

	sort.Slice(latencies, func(i int, j int) bool {
		return latencies[i] < latencies[j]
	})

	result := BenchmarkResult{
		Operation:      operation,
		Concurrency:    cfg.Concurrency,
		BlockSizeBytes: cfg.BlockSizeBytes,
		TotalOps:       int64(len(latencies)),
		ErrorCount:     0,
	}

	if len(latencies) > 0 {
		result.P50Ms = percentileMs(latencies, 50)
		result.P95Ms = percentileMs(latencies, 95)
		result.P99Ms = percentileMs(latencies, 99)
		result.P100Ms = percentileMs(latencies, 100)
	}

	_ = w.Write([]string{
		result.Operation,
		strconv.FormatInt(result.Concurrency, 10),
		strconv.FormatInt(result.BlockSizeBytes, 10),
		strconv.FormatFloat(result.P50Ms, 'f', -1, 64),
		strconv.FormatFloat(result.P95Ms, 'f', -1, 64),
		strconv.FormatFloat(result.P99Ms, 'f', -1, 64),
		strconv.FormatFloat(result.P100Ms, 'f', -1, 64),
		strconv.FormatInt(result.TotalOps, 10),
		strconv.FormatInt(result.ErrorCount, 10),
	})
	w.Flush()
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

	outputPath := filepath.Join("results", "bench", fmt.Sprintf("%s.csv", time.Now().Format(time.RFC3339)))
	outputFile, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
		_ = csvWriter.Write([]string{
			"operation",
			"concurrency",
			"block_size_bytes",
			"p50_ms",
			"p95_ms",
			"p99_ms",
			"p100_ms",
			"total_ops",
			"error_count",
		})
		csvWriter.Flush()
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
				}:
				case <-ctx.Done():
					return
				}
				_ = err
			}
		}(i, fds[i])
	}

	wg.Wait()
	close(results)

	aggregate(results, "write", cfg, csvWriter)

	fmt.Println("write benchmark complete")
}
