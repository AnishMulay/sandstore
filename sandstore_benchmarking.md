# Sandstore Benchmarking

# Context Block

## Architectural Scope & Objective

Implement a standalone benchmark binary (`clients/bench/main.go`) for the Sandstore hyper-converged architecture. The binary uses the existing `sandlib` client library to drive configurable POSIX workloads against a live Kubernetes cluster, measures client-observed latency directly, and appends a summary row to a CSV file. The cluster runs on a dedicated Ubuntu machine; the benchmark client runs on a MacBook M4 connecting over the local network via static seed addresses. The target is a self-contained, reproducible benchmark that produces publication-quality results for a vision paper.

## The Core Problem

Sandstore has a working hyper-converged architecture with full telemetry instrumentation and a functioning Kubernetes cluster setup, but no structured way to measure its performance characteristics. The smoke tests prove correctness; they prove nothing about latency, throughput, or how the system behaves under concurrent load. Without benchmark numbers, the vision paper has no empirical foundation and the project cannot make credible claims about what it delivers.

## Technical Background & Rationale

- The `sandlib` client (`clients/library`) is the correct entry point. It handles topology bootstrap, gRPC transport, and internal write buffering (2 MiB buffer, 8 MiB RPC chunk). All benchmark measurements are client-observed wall-clock latency. That is the most honest number to publish because it includes network, serialization, server processing, and replication.
- **Benchmark classification**: Steady-state load benchmark. A fixed goroutine pool runs for a fixed duration (60s default), self-generating load as fast as possible. This is not a push-to-failure load test and not a microbenchmark.
- **Concurrency model**: N independent goroutines each run an infinite loop issuing operations. Shutdown is coordinated via `context.WithTimeout`. When the context cancels, workers stop issuing new requests but finish their current in-flight RPC. This prevents coordinated omission and avoids artificially truncating tail latency.
- **Result collection**: Workers send latency samples (int64 nanoseconds) to a dedicated buffered results channel. Aggregation happens post-run after the channel is closed. No shared slice, no mutex contention.
- **Pre-warming**: One global warmup happens after client creation and before any benchmark phase. The binary issues 10 sequential `Stat("/")` calls to ensure the gRPC connection is fully established. No per-phase warmup is performed.
- **Two write modes** (required due to 2 MiB internal client buffer):
    - *API-Observed Mode* (small payloads, e.g. 4 KiB): measures user-perceived latency. Dominated by fast local memory appends, with rare large spikes when a buffer flush triggers an RPC.
    - *Flush-Forced Mode* (large payloads, e.g. 8 MiB+): exceeds buffer size, triggers immediate RPC, provides a clean approximation of true end-to-end network and storage-path cost.
- Operations in scope: **Write, Read, Stat, Create (Open+Close), ListDir**. Destructive ops (Remove, Rename, Rmdir) and Prometheus scraping layer explicitly deferred.
- Binary location follows existing convention: `clients/bench/main.go`, built as `bin/bench`.
- **Deployment**: cluster on Ubuntu gaming laptop via Kubernetes (`make cluster-up`). Benchmark client on MacBook M4 connecting over the local network via static seed addresses passed to `sandlib.NewSandstoreClient`.

## Study Session Goals: Completed ✓

1. Fair, repeatable distributed system benchmark design: measurement error sources and controls ✓
2. Latency percentiles (p50/p95/p99): computation and interpretation ✓
3. Go goroutine pool concurrency model: worker loops, context cancellation, WaitGroup ✓
4. Client-side buffering effects: two-mode write strategy crystallized ✓
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
- US-5: As a developer, if internal server errors occur during a run they are recorded in the CSV output and the benchmark continues. Other goroutines are unaffected.

## Failure Path Stories (AI-Generated)

- US-F1: As a developer, the `results/` directory does not exist on the client machine when I run the benchmark. The binary creates it automatically rather than crashing.
- US-F2: As a developer, multiple benchmark phases complete within one invocation. The binary appends a new row for each phase to the same timestamped CSV file rather than overwriting earlier phase rows.

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
// Seeds are passed as a single comma-separated string via the --seeds flag.
// Example: --seeds=192.168.1.10:8080,192.168.1.11:8080. The binary splits on
// comma to produce []string.
// Concurrency, duration, and block size are provided as CLI flags.
type BenchmarkConfig struct {
    Seeds           []string
    Concurrency     int64
    DurationSeconds int64 // default 60, configurable via --duration flag
    BlockSizeBytes  int64 // passed via --block-size flag, e.g. 4096, 65536, 1048576, 8388608
}

// BenchmarkResult represents the aggregated output of one complete benchmark phase.
// This maps directly to one row in the output CSV.
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
```

**Design notes:**

- `LatencyNanoseconds` is `int64`: raw `time.Duration` value, with no floating point representation error. Conversion to milliseconds happens at aggregation time only.
- Duration is exposed as a `--duration` flag with a default of 60 seconds.
- One benchmark invocation uses one block size. The developer is responsible for invoking the binary multiple times with different `--block-size` values to collect a dataset across sizes. Typical values: 4 KiB (4096), 64 KiB (65536), 1 MiB (1048576), 8 MiB (8388608).
- `BenchmarkResult.P100Ms` is the maximum observed latency, which is the single worst sample in the run.
- Operations run sequentially, one at a time. No generic operation interface is needed because each operation is called directly in its own benchmark phase.
- The Read benchmark uses files written during the Write benchmark phase. This is a hard ordering constraint: Write must complete fully before Read opens its measurement window.
- The pre-flight health check (`Stat` on root) runs before any benchmark phase. If it fails, the binary exits immediately, and no `BenchmarkResult` is written.

---

# Codebase Impact & Blast Radius

## New Files

- `clients/bench/main.go`: the benchmark binary. It is named `bench` to distinguish it from future benchmark suites (e.g. `bench_v2`, `bench_filesystem`). It follows the existing `clients/` binary convention.

## Modified Files

- `Makefile`: add a `make bench` target that builds `bin/bench` and runs it with the appropriate flags.
  The `make bench` target accepts `SEEDS` and `CONCURRENCY` as make variables and passes them through as `--seeds` and `--concurrency` flags to `bin/bench`. Example: `make bench SEEDS=192.168.1.10:8080,192.168.1.11:8080 CONCURRENCY=8`

## Strictly Off-Limits

- Everything else in the repository. No changes to `clients/library`, `servers/`, `internal/`, `integration/`, `deploy/`, `proto/`, or any existing client binary.

---

# Stack

Go. Standard library only (`encoding/csv`, `context`, `sync`, `sort`, `time`, `os`, `fmt`). No external dependencies.

---

# Future

- **Filesystem comparison experiments**: This benchmark suite acts as the execution harness for the next phase. It can run the same workloads against Sandstore clusters backed by different underlying filesystems (ext4, btrfs, etc.) on the Ubuntu machine. No code changes are required.
- **Blueprint for future benchmark suites**: The structure of this binary, including the goroutine pool, context cancellation, channel-based result collection, and CSV output, becomes the pattern for subsequent benchmarking work in the project.
- **Optimization feedback loop**: Combined with the existing Prometheus telemetry already wired through the hyper-converged architecture, benchmark results can open concrete avenues for performance optimization by correlating client-observed latency with server-side internal metrics.

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

## Flow 1: Setup Helper, `openFiles`

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

**Note**: Called with `O_CREATE|O_WRONLY|O_TRUNC` for the Write phase, `O_RDONLY` for the Read phase using the same file name pattern, and `O_CREATE` for the Stat phase setup.

If `openFiles` returns an error at any point before the timed loop begins, the binary logs the error to stderr and calls `os.Exit(1)`. No CSV row is written for a phase that fails during setup.

---

## Flow 2: Write Benchmark Phase

```go
// 1. Open/create one file per worker before the measurement window opens
fds, err := openFiles(client, cfg.Concurrency, "bench_write", O_CREATE|O_WRONLY|O_TRUNC)
if err != nil {
    fmt.Fprintf(os.Stderr, "write phase setup failed: %v\n", err)
    os.Exit(1)
}

// 2. Create results channel and context
results := make(chan Sample, cfg.Concurrency*1000)
ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.DurationSeconds)*time.Second)
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

            bytes := generateBytes(cfg.BlockSizeBytes) // fixed payload for this run
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

**EOF handling**: If a worker's `Read` call returns EOF, the worker seeks back to offset 0 by closing and reopening the file descriptor before the next iteration. This ensures reads continue to produce valid latency samples for the full duration without hitting EOF and stalling.

---

## Flow 4: Stat, Create, ListDir Phases

Same goroutine pool pattern. The Stat phase performs a setup step before the timed loop; the ListDir phase does not require file descriptor setup.

```go
// Stat: before the measurement window opens, create one dedicated file per
// worker using openFiles(client, cfg.Concurrency, "bench_stat", O_CREATE).
// The timed loop then calls Stat on each worker's file path by name. These
// files are created in the setup step, not inside the timed loop.
client.Stat(fmt.Sprintf("bench_stat_worker_%d", workerID))

// Create: Open with O_CREATE each iteration, close immediately
fd, _ := client.Open(fmt.Sprintf("/bench_create_%d_%d", workerID, iteration), O_CREATE)
client.Close(fd)

// ListDir: call ListDir on root each iteration
client.ListDir("/")
```

Each phase records samples with the appropriate operation label and follows the same context cancellation and results channel pattern.

---

## Flow 5: Aggregation and CSV Write

Called after each phase's results channel is closed. One summary CSV row is written per benchmark phase. Each of the five phases (Write, Read, Stat, Create, ListDir) produces exactly one CSV row when it completes.

```go
func aggregate(results <-chan Sample, operation string, cfg BenchmarkConfig, w *csv.Writer) {
    var latencies []int64
    var errorCount int64
    for s := range results {
        latencies = append(latencies, s.LatencyNanoseconds)
    }

    if len(latencies) == 0 {
        fmt.Fprintf(os.Stderr, "warning: phase %s produced zero samples; skipping CSV row\n", operation)
        return
    }

    sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

    result := BenchmarkResult{
        Operation:      operation,
        Concurrency:    cfg.Concurrency,
        BlockSizeBytes: cfg.BlockSizeBytes,
        P50Ms:          percentileMs(latencies, 50),
        P95Ms:          percentileMs(latencies, 95),
        P99Ms:          percentileMs(latencies, 99),
        P100Ms:         percentileMs(latencies, 100),
        TotalOps:       int64(len(latencies)),
        ErrorCount:     errorCount,
    }

    // Write one row to CSV
    w.Write([]string{
        result.Operation,
        fmt.Sprintf("%d", result.Concurrency),
        fmt.Sprintf("%d", result.BlockSizeBytes),
        fmt.Sprintf("%.4f", result.P50Ms),
        fmt.Sprintf("%.4f", result.P95Ms),
        fmt.Sprintf("%.4f", result.P99Ms),
        fmt.Sprintf("%.4f", result.P100Ms),
        fmt.Sprintf("%d", result.TotalOps),
        fmt.Sprintf("%d", result.ErrorCount),
    })
    w.Flush()
}
```

**CSV path**: `results/bench/<timestamp>.csv`. Directory is created if it does not exist. A new uniquely-timestamped file is created per binary invocation and opened in append mode so rows from earlier phases in the same invocation are preserved.

## Additional Design Decisions

- `generateBytes`: always returns a zero-filled byte slice of the requested size. The same buffer is reused across iterations for efficiency.
- `percentileMs`: uses the nearest-rank method. Sort the `int64` nanosecond slice ascending, then `index = ceil(p/100 * n) - 1`.
- CSV header: written only when the file is newly created (i.e., file size is 0 after opening in append mode). Never written on subsequent appends.
- Empty sample set: if a phase produces zero samples (e.g., duration too short), skip writing a CSV row and log a warning to stderr.
- Warmup: one global warmup after client creation, before any benchmark phase. Issue 10 `Stat("/")` calls sequentially to ensure the gRPC connection is fully established. No per-phase warmup.
- Result collection: post-run aggregation only. Workers send samples to the buffered channel during the run. After `wg.Wait()` and `close(results)`, the `aggregate` function drains the closed channel.
- Error handling: errors during an operation are counted but not fatal. The latency of a failed call is still recorded as a sample. A separate `error_count` field is added to `BenchmarkResult` and written as the last column in the CSV row.
- Phase order: `Write -> Read -> Stat -> Create -> ListDir`. This order is fixed and not configurable.
- File namespace: stable names (`bench_write_worker_0`, `bench_read_worker_0`, etc.). On each invocation, files are truncated/overwritten at setup time to ensure a clean state.
- Create phase: each iteration uses a unique name via an atomic counter (`bench_create_worker_{workerID}_{iteration}`). Files are not cleaned up during the run.
- CSV output: a new uniquely-timestamped file is created per binary invocation (e.g., `results/bench/2026-03-26T15-04-05.csv`). Multiple rows are appended to the same file within one invocation (one per phase).

---
