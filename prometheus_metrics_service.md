# Section 0: AI Directive & Coding Guidelines

This section is addressed to the coding AI implementing this feature. These directives are mandatory.

## 1. The Golden Rule

This SDDD markdown file is the absolute single source of truth for this feature. You do not invent logic, structs, interfaces, behaviors, or wiring that are not defined here. If any requirement, type, or implementation detail is unclear, you stop and ask before proceeding.

## 2. Authorized File List

You are only permitted to create or modify the files listed in Section 7 under **Authorized File List**. You do not touch any other file under any circumstances. If you believe a change is necessary in a file not on that list, you stop and ask before proceeding.

## 3. Rules of Engagement for This Feature

- The `MetricsService` interface must not depend on or import any Prometheus-specific types.
- Do not invent new metric constants. Only use the constants defined in the metrics package files.
- Do not modify the metadata service interface itself. Only the `BoltMetadataService` implementation may be changed.
- Do not add error returns to any `MetricsService` method. Silent drop is the defined behavior throughout.
- The `Start()` method on `PrometheusMetricsService` is called by the server constructor. It must never be called internally by the struct itself.
- Every new service instrumented must follow the per-service histogram naming convention: `sandstore_<service_snake_case>_latency_seconds`. Do not create shared histograms. Do not deviate from this naming convention under any circumstances.

## 4. Execution Rules

Implement one step at a time and wait for user confirmation before proceeding to the next step. Do not batch multiple changes in a single response.

# MetricsService Interface and Prometheus Implementation

# Goals and Non-Goals

## Goals

- add a metrics service interface so developers can collect metrics from the servers running as a part of the sandstore cluster.
- add a prometheus implementation of this interface so that the project is ready for benchmarking.
- add the prometheus implementation to the current hyperconverged architecture sandstore server implementation as a part of all services involved, inject it into the entire dependency chain.
- add a no-op interface implementation for no metrics.

## Non-Goals

- configuring a production Prometheus deployment is out of scope. A minimal Kubernetes Prometheus setup sufficient to verify the MVP works is in scope
- do not add or think about tracing, this design doc is just for metrics and metric collection.
- this design doc does not include metric aggregation, it just includes the MetricsService interface and a prometheus implementation.
- Nothing related to the benchmarking is a part of this design doc

---

# Data Models

```go
type MetricTags struct {
    Operation  string
    Service    string
    Additional map[string]string
}

type CounterName     string
type ObservationName string
type GaugeName       string

type MetricsService interface {
    Increment(name CounterName, value float64, tags MetricTags)
    Observe(name ObservationName, value float64, tags MetricTags)
    Gauge(name GaugeName, value float64, tags MetricTags)
}
```

Each service gets its own `HistogramVec` registered in `NewPrometheusMetricsService`. Naming convention: `sandstore_<service_snake_case>_latency_seconds`. No shared histograms. One `ObservationName` constant per service in its metrics constants file.

```go
// metrics/metadata.go
const (
    MetadataOperationLatency ObservationName = "metadata_operation_latency"
)

// metrics/simple_server.go
const (
    SimpleServerHandleMessageLatency ObservationName = "simple_server_handle_message_latency"
)
```

---

# Spike and Validate

**Chosen service:** `BoltMetadataService` (hyper-converged architecture)

**Scope:** Wire the Prometheus `MetricsService` implementation into `BoltMetadataService` only. Emit an `ObservationName` latency metric for every method exposed by the metadata service interface. Stand up the `/metrics` HTTP endpoint.

**Exit criterion:** Every method on the metadata service interface emits a latency metric. Hitting the `/metrics` endpoint — either via `curl` or the Prometheus expression browser — shows those metrics with real values after exercising the service.

**What to document afterward:** Any interface changes required, any unexpected wiring complexity, and which patterns from this service can be mechanically repeated across remaining services.

---

# Future

This feature is the first step in a broader observability stack for Sandstore. It unlocks three things directly:

1. **Benchmarking suite.** With latency and throughput metrics emitted from within the system, the next project cycle can build a structured benchmarking harness targeting POSIX-compliant operations on the hyper-converged node. This is the immediate successor to this feature.
2. **White paper and public writing.** Verified benchmark results from a fully instrumented system are the foundation for a vision paper on Sandstore's architecture and performance characteristics, as well as technical blog content.
3. **Distributed tracing.** The same injection pattern used here — interface defined at the composition root, threaded through the dependency chain — applies directly to a future tracing service. This feature proves the pattern works before tracing adds complexity.

---

# Flows

### Server Construction

```go
func NewPrometheusMetricsService(port string) *PrometheusMetricsService {
    metadataServiceLatencyHistogram := promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "sandstore_metadata_service_latency_seconds",
            Help: "Histogram of latency for Sandstore metadata service operations",
        },
        []string{"operation", "service"},
    )
    simpleServerLatencyHistogram := promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "sandstore_simple_server_latency_seconds",
            Help: "Histogram of latency for Sandstore SimpleServer operations",
        },
        []string{"operation", "service"},
    )
    histograms := map[ObservationName]*prometheus.HistogramVec{
        MetadataOperationLatency:         metadataServiceLatencyHistogram,
        SimpleServerHandleMessageLatency: simpleServerLatencyHistogram,
        // Every future service follows the same pattern:
        // <ServiceObservationName>: sandstore_<service_snake_case>_latency_seconds
    }
    return &PrometheusMetricsService{
        port:       port,
        histograms: histograms,
    }
}

// Start is called by the server constructor, not internally.
// The caller owns the lifecycle.
func (p *PrometheusMetricsService) Start() {
    http.Handle("/metrics", promhttp.Handler())
    http.ListenAndServe(p.port, nil)
}

func BuildServer(...) *Server {
    metricsService := metrics.NewPrometheusMetricsService(":2112")
    go metricsService.Start() // caller starts the HTTP server
    boltMetadataService := NewBoltMetadataService(filePath, metricsService)
    // ... continue wiring remaining services
}
```

### Flow 2 — Instrumented method (template for all six methods)

```go
func (b *BoltMetadataService) ApplyCreate(req CreateRequest) (CreateResponse, error) {
    start := time.Now()
    resp, err := b.executeCreateLogic(req)
    elapsed := time.Since(start).Seconds()
    tags := metrics.MetricTags{
        Operation: string(MetadataServiceOperationCreate), // from existing operations constants
        Service:   "BoltMetadataService",
    }
    // NOTE: Status (success vs error) is deliberately not captured in tags.
    // Status field was removed from MetricTags by design decision — silent drop on error.
    b.metricsService.Observe(metrics.MetadataOperationLatency, elapsed, tags)
    return resp, err
}
```

### Flow 3 — PrometheusMetricsService.Observe

```go
func (p *PrometheusMetricsService) Observe(name ObservationName, value float64, tags MetricTags) {
    histogram, exists := p.histograms[name]
    if !exists {
        return // silent drop — unregistered metric name
    }
    // label order must match registration order: {"operation", "service"}
    histogram.WithLabelValues(tags.Operation, tags.Service).Observe(value)
}
```

---

# Codebase impact and blast radius

**New Files**

- `internal/metrics/interfaces.go` — `MetricsService` interface, `MetricTags` struct, typed name types
- `internal/metrics/metadata.go` — `MetadataOperationLatency` constant
- `internal/metrics/prometheus/prometheus_metrics_service.go` — Prometheus implementation
- `deploy/kubernetes/prometheus.yaml` — minimal Prometheus scrape config for MVP verification

**Modified Files**

- `internal/metadata/bolt_metadata_service.go` — add optional `MetricsService` field to struct, wrap all six methods with timing and observe calls
- `internal/server/wire_grpc_etcd.go` — create `PrometheusMetricsService`, call `Start()` in a goroutine, pass to `BoltMetadataService` constructor

**Authorized File List — Coding AI may only touch these files:**

- `internal/metrics/interfaces.go`
- `internal/metrics/metadata.go`
- `internal/metrics/prometheus/prometheus_metrics_service.go`
- `internal/metadata/bolt_metadata_service.go`
- `internal/server/wire_grpc_etcd.go`
- `deploy/kubernetes/prometheus.yaml`
- `scripts/dev/cluster-up.sh`
- `scripts/dev/cluster-down.sh`
- `deploy/k8s/prometheus-configmap.yaml`
- `deploy/k8s/deployment-prometheus.yaml`
- `deploy/k8s/service-prometheus.yaml`
- `Makefile` — add four new targets only: `cluster-up`, `cluster-down`, `smoke-test`, `port-forward-prometheus`

Any file not on this list must not be created or modified. If a change seems necessary in any other file, stop and ask before proceeding.

---

# Testing

**Test Harness Architecture**

The existing test infrastructure in `scripts/dev/test-cluster.sh` and `integration/cluster/cluster_test.go` is reused as the foundation. A new persistent cluster target is added alongside it — separate from `make test-cluster`, which is ephemeral and not reused here.

**Cluster Setup**

`make cluster-up` invokes `scripts/dev/cluster-up.sh`, which:

1. Creates the namespace
2. Applies `storageclass.yaml`, `service-etcd.yaml`, `statefulset-etcd.yaml` — waits for etcd rollout
3. Applies `configmap.yaml`, `service-headless.yaml`, `rbac-cluster-tests.yaml`, `job-bootstrap.yaml`
4. Applies `statefulset-sandstore.yaml` — waits for rollout
5. Applies `prometheus-configmap.yaml`, `deployment-prometheus.yaml`, `service-prometheus.yaml`

**Leader Election Wait**

Reuse `waitForLeader` from `integration/cluster/helpers.go` — polls all three seeds via `topology_request`, requires one leader reported by at least two seeds, winning three polling rounds in a row two seconds apart, within a two minute timeout. This runs as `TestLeaderElectionReady` before the smoke test executes.

**Smoke Test**

`make smoke-test` runs `clients/open_smoke/main.go` as a Kubernetes Job inside the cluster — not from the host machine — because internal leader addresses (`sandstore-0.sandstore-headless:8080`) are not externally routable. The job is seeded with `SANDSTORE_SEEDS=sandstore-0...,sandstore-1...,sandstore-2...`.

The smoke test must exercise all six metadata operations: create, remove, rename, update, apply, and set_attribute. *(set_attribute coverage depends on your decision above.)*

**Metrics Verification**

`make port-forward-prometheus` forwards `localhost:9090` to the Prometheus pod by label. After the smoke test runs, open `localhost:9090` in a browser and query `sandstore_operation_latency_seconds` in the expression browser.

**Pass Criterion**

The test passes when: the smoke test exits cleanly AND the Prometheus expression browser shows non-zero histogram bucket counts for each of the six metadata operations, tagged with the correct `operation` and `service` label values.

**Teardown**

`make cluster-down` invokes the teardown script — deletes the namespace, waits up to 240s for namespace deletion, polls until no PVs still reference the namespace. Covers both `app=sandstore` and `app=etcd` PVCs, unlike the existing `k8s-destroy` target which only handles sandstore PVCs.

---

# Stack

The entire project is in Go, so all the changes associated with this will also be in Go, bash or Kubernetes and make commands.
