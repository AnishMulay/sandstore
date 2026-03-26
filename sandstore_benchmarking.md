# Sandstore Benchmarking

# Context Block

## Architectural Scope & Objective

Implement a standalone benchmark binary (`clients/bench/main.go`) for the Sandstore hyper-converged architecture. The binary uses the existing `sandlib` client library to drive configurable POSIX workloads against a live Kubernetes cluster, measures client-observed latency directly, and appends a summary row to a CSV file. The cluster runs on a dedicated Ubuntu machine; the benchmark client runs on a MacBook M4 connecting over the local network via static seed addresses. The target is a self-contained, reproducible benchmark that produces publication-quality results for a vision paper.

## The Core Problem

Sandstore has a working hyper-converged architecture with full telemetry instrumentation and a functioning Kubernetes cluster setup, but no structured way to measure its performance characteristics. The smoke tests prove correctness; they prove nothing about latency, throughput, or how the system behaves under concurrent load. Without benchmark numbers, the vision paper has no empirical foundation and the project cannot make credible claims about what it delivers.

## Technical Background & Rationale

- The `sandlib` client (`clients/library`) is the correct entry point. It handles topology bootstrap, gRPC transport, and internal write buffering (2 MiB buffer, 8 MiB RPC chunk). All benchmark measurements are client-observed wall-clock latency — the most honest number to publish because it includes network, serialization, server processing, and replication.
- **Benchmark classification**: Steady-state load benchmark. A fixed goroutine pool runs for a fixed duration (60s default), self-generating load as fast as possible. This is not a push-to-failure load test and not a microbenchmark.
- **Concurrency model**: N independent goroutines each run an infinite loop issuing operations. Shutdown is coordinated via `context.WithTimeout`. When the context cancels, workers stop issuing new requests but finish their current in-flight RPC — this prevents coordinated omission and avoids artificially truncating tail latency.
- **Result collection**: Workers send latency samples (int64 nanoseconds) to a dedicated buffered results channel. A separate collector goroutine reads from the channel to aggregate percentiles. No shared slice, no mutex contention.
- **Pre-warming**: gRPC connection setup (TCP + HTTP/2 handshake) and a short warmup phase of real operations happen before the measurement window opens. This prevents artificial p99 inflation from connection cost being included in results.
- **Two write modes** (required due to 2 MiB internal client buffer):
    - *API-Observed Mode* (small payloads, e.g. 4 KiB): measures user-perceived latency. Dominated by fast local memory appends, with rare large spikes when a buffer flush triggers an RPC.
    - *Flush-Forced Mode* (large payloads, e.g. 8 MiB+): exceeds buffer size, triggers immediate RPC, provides a clean approximation of true end-to-end network and storage-path cost.
- Output is one summary CSV row per run: `operation, concurrency, block_size_bytes, duration_sec, total_ops, throughput_ops_sec, throughput_mb_sec, p50_ms, p95_ms, p99_ms`. Aggregation happens inside the binary; raw per-sample data is not persisted.
- Operations in scope: **Write, Read, Stat, Create (Open+Close), ListDir**. Destructive ops (Remove, Rename, Rmdir) and Prometheus scraping layer explicitly deferred.
- Binary location follows existing convention: `clients/bench/main.go`, built as `bin/bench`.
- **Deployment**: cluster on Ubuntu gaming laptop via Kubernetes (`make cluster-up`). Benchmark client on MacBook M4 connecting over the local network via static seed addresses passed to `sandlib.NewSandstoreClient`.

## Study Session Goals — Completed ✓

1. Fair, repeatable distributed system benchmark design — measurement error sources and controls ✓
2. Latency percentiles (p50/p95/p99) — computation and interpretation ✓
3. Go goroutine pool concurrency model — worker loops, context cancellation, WaitGroup ✓
4. Client-side buffering effects — two-mode write strategy crystallized ✓
5. CSV output conventions for pandas consumption ✓

---

# Goals & Non-Goals

## Goals

- Implement a benchmark binary that measures client-observed end-to-end latency for five POSIX operations exposed by the `sandlib` client library: **Write, Read, Stat, Create (Open+Close), and ListDir**.
- The benchmark client runs on a separate machine from the Sandstore cluster, measuring true over-the-network latency rather than co-located performance.
- The benchmark is buildable and runnable via a single `make` target on the client machine.
- Results are written to a CSV file in a format suitable for analysis and chart generation in pandas or a spreadsheet.

## Non-Goals

- No Prometheus-based metrics collection or server-side instrumentation. This benchmark measures only client-observed latency.
- No comparison of performance across different underlying filesystems. That experiment is deferred to a future phase.
- No cluster infrastructure setup or configuration. The benchmark assumes a running, reachable Sandstore cluster.
- No push-to-failure load testing or capacity planning. This is a steady-state latency benchmark, not a stress test.

---

# User Stories

## Happy Path Stories (User-Authored)

- US-1: As a developer, I run `make bench` with seed addresses as input and the benchmark executes against the Sandstore cluster and exits cleanly.
- US-2: As a developer, after a benchmark run a CSV file is written to a timestamped path under `results/` that I can use for analysis and charting.
- US-3: As a developer, the benchmark code is readable enough that I can follow the existing pattern to add a new operation without touching the concurrency or CSV logic.
- US-4: As a developer, before the benchmark starts a pre-flight health check runs, and if the cluster is unreachable the binary exits immediately with a clear error message.
- US-5: As a developer, if internal server errors occur during a run they are recorded in the CSV output and the benchmark continues — other goroutines are unaffected.

## Failure Path Stories (AI-Generated)

- US-F1: As a developer, the `results/` directory does not exist on the client machine when I run the benchmark — the binary creates it automatically rather than crashing.
- US-F2: As a developer, a results CSV from a prior run already exists at the target path — the binary appends a new row rather than overwriting it, preserving my historical data.

---

# Data Models & Interfaces

```go
// Sample represents a single latency observation from one worker goroutine.
type Sample struct {
    WorkerID           int
    Operation          string
    LatencyNanoseconds int64
}

// BenchmarkConfig holds all runtime configuration for a benchmark run.
// WriteSizes is hardcoded in the binary — not a flag.
// Seeds and Concurrency are provided as CLI flags.
type BenchmarkConfig struct {
    Seeds       []string
    Concurrency int64
    WriteSizes  []int64
}

// BenchmarkResult represents the aggregated output of one complete benchmark run.
// This maps directly to one row in the output CSV.
type BenchmarkResult struct {
    Operation   string
    Concurrency int64
    P50Ms       float64
    P95Ms       float64
    P99Ms       float64
    P100Ms      float64
    TotalOps    int64
}
```

**Design notes:**

- `LatencyNanoseconds` is `int64` — raw `time.Duration` value, no floating point representation error. Conversion to milliseconds happens at aggregation time only.
- `WriteSizes` is a hardcoded list in the binary source. A developer who wants to change payload sizes edits the code directly.
- `BenchmarkResult.P100Ms` is the maximum observed latency — the single worst sample in the run.
- Operations run sequentially, one at a time. No generic operation interface is needed — each operation is called directly in its own benchmark phase.
- The Read benchmark uses files written during the Write benchmark phase. This is a hard ordering constraint: Write must complete fully before Read opens its measurement window.
- The pre-flight health check (`Stat` on root) runs before any benchmark phase. If it fails, the binary exits immediately — no `BenchmarkResult` is written.

---

# Codebase Impact & Blast Radius

## New Files

- `clients/bench/main.go` — the benchmark binary. Named `bench` to distinguish it from future benchmark suites (e.g. `bench_v2`, `bench_filesystem`). Follows the existing `clients/` binary convention.

## Modified Files

- `Makefile` — add a `make bench` target that builds `bin/bench` and runs it with the appropriate flags.

## Strictly Off-Limits

- Everything else in the repository. No changes to `clients/library`, `servers/`, `internal/`, `integration/`, `deploy/`, `proto/`, or any existing client binary.

---

# Stack

Go. Standard library only (`encoding/csv`, `context`, `sync`, `sort`, `time`, `os`, `fmt`). No external dependencies.

---

# Future

- **Filesystem comparison experiments**: This benchmark suite acts as the execution harness for the next phase — running the same workloads against Sandstore clusters backed by different underlying filesystems (ext4, btrfs, etc.) on the Ubuntu machine. No code changes required; the benchmark runs as-is.
- **Blueprint for future benchmark suites**: The structure of this binary — goroutine pool, context cancellation, channel-based result collection, CSV output — becomes the pattern for all subsequent benchmarking work in the project.
- **Optimization feedback loop**: Combined with the existing Prometheus telemetry already wired through the hyper-converged architecture, benchmark results can open concrete avenues for performance optimization — correlating client-observed latency with server-side internal metrics.

---

# Component Flows

## Flow 0: Pre-Flight Health Check

```go
// 1. Initialize the sandlib client with seed addresses from flags
client, err := sandlib.NewSandstoreClient(cfg.Seeds, comm)
if err != nil {
    fmt.Fprintf(os.Stderr, "pre-flight failed: could not connect to cluster: %v\n", err)
    os.Exit(1)
}

// 2. Stat root to confirm cluster is reachable and responsive
_, err = client.Stat("/")
if err != nil {
    fmt.Fprintf(os.Stderr, "pre-flight failed: cluster unreachable: %v\n", err)
    os.Exit(1)
}
// Pre-flight passed. Proceed to benchmarking.
```

**Ordering constraint**: Pre-flight must complete successfully before any benchmark phase begins. No CSV row is written if pre-flight fails.

---

## Flow 1: Setup Helper — openFiles

Called before each benchmark phase that requires file descriptors. Creates or opens one file per worker goroutine.

```go
func openFiles(client *sandlib.SandstoreClient, concurrency int, prefix string, flags int) ([]int, error) {
    fds := make([]int, concurrency)
    for i := 0; i < concurrency; i++ {
        fd, err := client.Open(fmt.Sprintf("%s_worker_%d", prefix, i), flags)
        if err != nil {
            return nil, fmt.Errorf("openFiles: failed to open file for worker %d: %w", i, err)
        }
        fds[i] = fd
    }
    return fds, nil
}
```

**Note**: Called with `O_CREATE` for the Write phase. Called with read flags for the Read phase using the same file name pattern, ensuring Read workers open the files that Write workers populated.

---

## Flow 2: Write Benchmark Phase

```go
// 1. Open/create one file per worker before the measurement window opens
fds, err := openFiles(client, cfg.Concurrency, "bench_write", O_CREATE|O_WRONLY)
if err != nil {
    // handle
}

// 2. Create results channel and context
results := make(chan Sample, cfg.Concurrency*1000)
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()

// 3. Spawn one goroutine per worker
var wg sync.WaitGroup
for i := 0; i < cfg.Concurrency; i++ {
    wg.Add(1)
    go func(workerID int, fd int) {
        defer wg.Done()
        for {
            // Check context before issuing next request
            select {
            case <-ctx.Done():
                return
            default:
            }

            bytes := generateBytes(writeSize) // fixed payload for this run
            start := time.Now()
            _, err := client.Write(fd, bytes)
            latency := time.Since(start).Nanoseconds()

            results <- Sample{
                WorkerID:           workerID,
                Operation:          "write",
                LatencyNanoseconds: latency,
            }
            // Note: errors are not fatal. Sample is recorded regardless.
            _ = err
        }
    }(i, fds[i])
}

// 4. Wait for all workers to finish, then close results channel
wg.Wait()
close(results)

// 5. Aggregate and write CSV row (see Flow 5)
aggregate(results, "write", cfg, csvWriter)
```

**Ordering constraint**: Write phase must complete fully before Read phase begins. fds from Write are closed before Read opens its own.

---

## Flow 3: Read Benchmark Phase

Identical structure to Write. Key differences:

```go
// Open same files written by Write phase, using same name pattern
fds, err := openFiles(client, cfg.Concurrency, "bench_write", O_RDONLY)

// Worker loop issues Read instead of Write
n, err := client.Read(fd, readSize)

// Sample operation label is "read"
results <- Sample{WorkerID: workerID, Operation: "read", LatencyNanoseconds: latency}
```

**Ordering constraint**: Read phase opens fresh file descriptors from offset 0. Write fds must be closed before Read opens the same files.

---

## Flow 4: Stat, Create, ListDir Phases

Same goroutine pool pattern. No file descriptor setup needed for Stat and ListDir.

```go
// Stat: call Stat on a known path each iteration
client.Stat(fmt.Sprintf("/bench_stat_%d", workerID))

// Create: Open with O_CREATE each iteration, close immediately
fd, _ := client.Open(fmt.Sprintf("/bench_create_%d_%d", workerID, iteration), O_CREATE)
client.Close(fd)

// ListDir: call ListDir on root each iteration
client.ListDir("/")
```

Each phase records samples with the appropriate operation label and follows the same context cancellation and results channel pattern.

---

## Flow 5: Aggregation and CSV Write

Called after each phase's results channel is closed.

```go
func aggregate(results <-chan Sample, operation string, cfg BenchmarkConfig, w *csv.Writer) {
    var latencies []int64
    for s := range results {
        latencies = append(latencies, s.LatencyNanoseconds)
    }

    sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

    result := BenchmarkResult{
        Operation:   operation,
        Concurrency: cfg.Concurrency,
        P50Ms:       percentileMs(latencies, 50),
        P95Ms:       percentileMs(latencies, 95),
        P99Ms:       percentileMs(latencies, 99),
        P100Ms:      percentileMs(latencies, 100),
        TotalOps:    int64(len(latencies)),
    }

    // Write one row to CSV
    w.Write([]string{
        result.Operation,
        fmt.Sprintf("%d", result.Concurrency),
        fmt.Sprintf("%.4f", result.P50Ms),
        fmt.Sprintf("%.4f", result.P95Ms),
        fmt.Sprintf("%.4f", result.P99Ms),
        fmt.Sprintf("%.4f", result.P100Ms),
        fmt.Sprintf("%d", result.TotalOps),
    })
    w.Flush()
}
```

**CSV path**: `results/bench/<timestamp>.csv`. Directory is created if it does not exist. File is opened in append mode — existing rows are preserved.

---