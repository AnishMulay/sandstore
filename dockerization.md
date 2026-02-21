# Dockerization

## Process

- [x]  Goals → list out the goals of the project.
- [x]  User stories → user centric design, 10 to 20 user stories for the project.
- [x]  Data models → define the data models and how the data models are going to be interacting with each other.
- [x]  Nail an MVP → what are the user stories which are required for a minimum viable product, get rid of the rest for now.
- [x]  Draw the prototype → paper is cheap, code is expensive.
- [x]  What does the future of the project look like ?
- [x]  Drill into the specific components.
- [ ]  Pick a stack → programming language, frameworks etc.
- [ ]  Actual software development.

---

## Goals

- **One-Click Local Deployment:** A developer or learner should be able to spin up a fully functional, multi-node Sand Store cluster locally using pre-built images with a single command.
- **Modular Build-Time Pipeline:** Create a flexible build system where users select their desired implementations (e.g., gRPC vs. HTTP) before building. The pipeline will generate specific, optimized, and lean Docker images containing only the selected components.
- **Build-Time Compatibility Validation:** The build pipeline must validate that the selected combination of modules is compatible before generating the images or deployment files, preventing broken deployments.
- **Strict Separation of Build and Deploy:** The pipeline that generates Docker images and deployment manifests (Kubernetes/Compose files) will be completely separate from the actual deployment process.
- **Environment Portability (The K8s Stepping Stone):** Ensure the containerized architecture is strictly decoupled from the host machine to make the future transition to Kubernetes frictionless.
- **Learning-Optimized Developer Experience (DX):** Keep the build configurations and deployment manifests readable and well-documented so they serve as an educational tool.

---

## User Stories

### 1. Persona: The Quick-Start Learner

*Focus: Zero friction, immediate gratification, clear visibility, and fault tolerance.*

- **Story 1.1 - The "Magic" Start:** As a learner, I want to run a single command (e.g., `docker compose up`) to start a fully functional, default 3-node Sand Store cluster (with the Etcd dependency included), so that I can see the distributed system running immediately without needing to install Go or compile binaries manually.
- **Story 1.2 - Out-of-the-box Testing:** As a learner, I want a pre-packaged client container (the "smoke client") included in the default environment, so that I can immediately trigger read/write/delete operations against the cluster to verify it works without setting up a local client.
- **Story 1.3 - Isolated & Readable Logs:** As a learner, I want the containerized cluster to output logs to standard Docker streams (filterable via `docker compose logs <service>`) or to isolated, mounted log files on my host machine, so that I can debug specific components without being overwhelmed by a massive, cluster-wide log stream.
- **Story 1.4 - Resilient Startup (Race Conditions):** As a learner, if a Sand Store node starts up before its dependencies (like Etcd) are ready, I want the container to gracefully retry or auto-restart via Docker health checks, so that the cluster eventually reaches a healthy state without manual intervention.
- **Story 1.5 - Clean Teardown / State Reset:** As a learner, I want a simple, documented command (e.g., `docker compose down -v` or `make clean`) that completely destroys all containers and wipes all persistent local volumes, so that I can easily reset the cluster to a blank state for my next test.

### 2. Persona: The Tinkerer / Experimenter / Maintainer

*Focus: Compile-time modularity, dependency guardrails, benchmarking, and cross-platform compatibility.*

- **Story 2.1 - Compile-Time Modularity:** As an experimenter, I want to pass build arguments (e.g., `COMMUNICATOR=http`) to the build pipeline, which will seamlessly translate into Go build tags (`//go:build http`), so that the system compiles and packages *only* the requested implementations, ensuring no dead code is bundled into the final binary.
- **Story 2.2 - Pre-Defined Build Profiles:** As an experimenter, I want to pass a single 'profile' flag (e.g., `PROFILE=production-raft`) that references a predefined matrix of known-compatible modules, so that I have a guaranteed working baseline before I start manually swapping individual components.
- **Story 2.3 - Auto-Resolving Dependencies (The Guardrails):** As an experimenter, if I manually select a specific core module, I want the build system to automatically bundle any strictly required dependent services (e.g., automatically pairing the Raft-specific Metadata service if I choose the Raft Cluster service), so that I don't accidentally build an incompatible node.
- **Story 2.4 - Failing Fast on Incompatibilities:** As an experimenter, if I explicitly select a combination of modules that are structurally incompatible or pass an invalid argument (e.g., a typo like `gprc`), I want the build script to instantly fail with a clear, descriptive error *before* wasting time compiling Go code or building Docker layers.
- **Story 2.5 - Hyper-Optimized Images for Benchmarking:** As a maintainer running load tests, I want the build pipeline to utilize multi-stage Docker builds to output minimal, static-binary images (e.g., using `scratch` or `alpine`), so that container OS overhead is virtually zero and I can accurately benchmark the Go application's performance.
- **Story 2.6 - Cross-Platform Compatibility:** As a maintainer, I want the build pipeline to explicitly support cross-compilation (handling `GOOS` and `GOARCH`), so that I can build the Docker images on an ARM64 machine (like an Apple Silicon Mac) but confidently deploy them to an AMD64 Linux cloud environment.
- **Story 2.7 - Customizing Cluster Topology:** As an experimenter, I want to use standard deployment overrides (e.g., `docker-compose.override.yml`) to easily scale the number of specific nodes (e.g., running 5 Chunk nodes instead of 3), so that I can test replication, consensus, and fault tolerance at different scales.

---

## Data Models

### Data Model 1: Container Runtime & Environment Variables

Since your Go app uses CLI flags, the Docker startup script will map these environment variables to those flags. We also must add an environment variable for Etcd.

| **Docker Env Var** | **Go CLI Flag Equivalent** | **Default Value** | **Description** |
| --- | --- | --- | --- |
| `NODE_ID` | `--node-id` | *(Required)* | Unique identifier for the node (e.g., `node-1`). |
| `LISTEN_ADDR` | `--listen` | `:8080` | Address/Port the node binds to. |
| `DATA_DIR` | `--data-dir` | `/var/lib/sandstore` | Path inside the container for chunks/logs. |
| `ETCD_ENDPOINTS` | *(Needs to be added)* | `etcd:2379` | **CRITICAL:** Replaces the hardcoded `localhost:2379` so nodes can find the Etcd container. |

### Data Model 2: The Etcd Control Plane State (JSON)

Because the nodes fetch their configuration and peers dynamically from Etcd, the build system needs to understand the JSON schema that lives inside Etcd under the `/sandstore/config/nodes/...` prefix.

JSON

`// Go Struct: cluster.ClusterNode
{
  "id": "node-1",
  "address": "node-1:8080",
  "role": "metadata-raft-member",
  "metadata": {
    "custom_key": "custom_value"
  }
}`

*Note: Our Docker setup will need an "init container" that runs your `init-etcd.sh` script to populate this JSON into the Etcd container before the Sand Store nodes boot up.*

### Data Model 3: The Smoke Test Deployment Topology (Docker Compose)

This is the "State" model of the entire cluster when a learner runs the automated test. We are mirroring the exact flow of your `run-smoke.sh` script, but fully containerized.

- **Layer 1: The Control Plane**
    - **Service: `etcd`** (Image: `quay.io/coreos/etcd:v3.5.0`)
    - **Service: `etcd-init`** (A temporary container that runs `init-etcd.sh` to populate the 3 nodes into Etcd, then exits).
- **Layer 2: The Sand Store Cluster**
    - **Services: `node-1`, `node-2`, `node-3`** (Depends on `etcd-init` completing successfully).
    - Bootstraps using: `/app/bin/sandstore --server node --node-id <id> --listen <id>:8080 ...`
    - Internally, the `RaftMetadataReplicator` will read the Etcd state, discover the other 2 nodes, and hold an election.
- **Layer 3: The Smoke Test**
    - **Service: `smoke-client`**
    - *Healthcheck Wait:* Pauses until it sees "Became Leader" in the logs of one of the nodes (or repeatedly polls).
    - *Execution:* Runs `SANDSTORE_ADDR="<leader_addr>" /app/bin/smoke`.

---

## MVP

The scope defined in the User Stories and Data Models represents the strict Minimum Viable Product (MVP) for Sand Store's containerization phase. It achieves the core requirements of compile-time modularity (via Go build tags), zero-os-overhead images (via multi-stage builds), and automated local cluster orchestration (via Docker Compose). We have intentionally excluded advanced orchestration (like Kubernetes or Helm charts) and centralized log aggregation (like ELK/Loki) from this phase to avoid premature complexity. Reducing the scope any further would either break the automated 3-node Raft testing flow or violate the project's learning-first modularity goals.

---

## Prototype

```jsx
======================================================================
HIGH-LEVEL DESIGN: THE SAND STORE BUILD & DEPLOYMENT PIPELINE
======================================================================

[PHASE 1: THE BUILD PROCESS]
User Input  -->  Makefile  -->  Docker Builder Stage  -->  Final Docker Image
(CLI)            (Orchestrates) (Compiles Go Code)         (Scratch/Alpine)
  |                 |                 |                           |
  v                 v                 v                           v
`make build    Parses profile,   `go build -tags="raft http"` Drops static binary
 PROFILE=raft` Injects Build     Strips out unused gRPC/Etcd  into empty OS image.
               Args (GOOS, etc)  code for a tiny binary.      Result: ~15MB Image.

                                     ||
                                     || (Image is passed to runtime)
                                     \/

[PHASE 2: THE RUNTIME TOPOLOGY (docker-compose up)]

                 [  Control Plane  ]
                 |                 |
                 v                 v
          +-------------+   +-------------+
          | etcd-server |<--| etcd-init   | (Ephemeral Script: injects 3-node
          | (:2379)     |   | container   |  Raft topology JSON into Etcd)
          +-------------+   +-------------+
                 ^                 
                 | (Dynamic Config Fetch)
                 |
      +-----------------------------------------+
      |        [ Sand Store Data Plane ]        |
      |                                         |
      |  +----------+  +----------+  +----------+ |
      |  |  Node 1  |  |  Node 2  |  |  Node 3  | | (Bootstraps via CLI flags,
      |  | (Leader) |<~> (Follower)<~> (Follower) |  discovers peers via Etcd,
      |  +----------+  +----------+  +----------+ |  holds Raft Election)
      +-----------------------------------------+
                 ^
                 | (Waits for "Became Leader" log)
                 |
          +-------------+
          | smoke-test  | (Executes file system operations 
          | container   |  against the elected Leader)
          +-------------+
```

---

## Project future

**1. Phase 2: Cloud-Native Orchestration (Kubernetes)**
While Docker Compose provides local consistency, the immediate next phase is migrating the deployment architecture to Kubernetes. This will involve:

- **StatefulSets & Persistent Volumes:** Moving away from local Docker bind mounts to Kubernetes `StatefulSets` with dynamically provisioned `PersistentVolumeClaims` (PVCs) to properly manage the state of the Etcd control plane and the Sand Store chunk nodes.
- **Helm Chart Creation:** Packaging the entire cluster topology (Etcd init scripts, Raft configurations, node scaling) into a single, configurable Helm chart for one-click deployment to any local (Minikube/k3s) or cloud-based (EKS/GKE) Kubernetes cluster.

**2. Hardware-Aware Storage Engine Benchmarking**
A primary goal of the containerized architecture is to enable isolated, reproducible hardware experiments. The first major sub-project will be a comparative analysis of underlying host filesystems on Flash/NVMe media.

- **The Experiment:** Deploying the Sand Store chunk services onto a bare-metal node (e.g., a repurposed gaming laptop with an SSD) and running intensive distributed I/O load tests.
- **The Variables:** Benchmarking the cluster's performance when the underlying Docker volumes are backed by traditional magnetic-optimized file systems (like `ext4`) versus flash-optimized file systems (like `f2fs` or `btrfs`).
- **The Output:** Generating public case studies on how the host OS file system impacts distributed chunk storage metrics, specifically focusing on write amplification, IOPS, and tail latency.

**3. Automated Performance & Chaos Engineering (The "LinkedIn" Metrics)**
To establish Sand Store as a serious educational distributed system, the project will expand to include automated benchmarking and failure testing pipelines.

- **Telemetry & Observability:** Integrating Prometheus and Grafana into the Docker/K8s environments to generate visual dashboards of cluster health, election times, and network throughput.
- **Chaos Testing:** Introducing network partition and node-kill scripts (using tools like Chaos Mesh) during active read/write loads to mathematically prove the Raft consensus and replication durability.
- **Public Benchmarks:** Publishing the results of these stress tests as technical deep-dives (articles/posts) to demonstrate practical expertise in distributed systems failure modes and performance tuning.

---

## Specific components

### Component 1: Go Code Refactors (The Enablers)

*Objective: Remove hardcoded values to allow for containerized networking and establish the compile-time modularity pattern.*

**1.1. Flow for Environment Variable Injection (`cmd/sandstore/main.go` & `server.go`)**

- **The Target:** The AI must refactor the configuration loading so that Docker environment variables override default values, but CLI flags override environment variables.
- **The Implementation Flow:**
    - In `main.go`, before calling `flag.Parse()`, the code must check `os.Getenv()`.
    - Specifically, parse `NODE_ID` (maps to `-node-id`), `LISTEN_ADDR` (maps to `-listen`), and `DATA_DIR` (maps to `-data-dir`).
    - **CRITICAL ETCD FIX:** Locate `server.go` (around line 63) where `NewEtcdClusterService` is instantiated with hardcoded `[]string{"localhost:2379"}`. The AI must extract this into a new configuration variable called `EtcdEndpoints`.
    - `EtcdEndpoints` must be read from the environment variable `ETCD_ENDPOINTS`. If `ETCD_ENDPOINTS` is empty, it must gracefully fall back to `"127.0.0.1:2379"`.
    - *Edge Case Handled:* This ensures the system still works perfectly on bare-metal laptops without Docker, while allowing Docker Compose to pass `ETCD_ENDPOINTS=etcd:2379`.

**1.2. Flow for Compile-Time Build Tags (`server.go` wire-up)**

- **The Target:** Establish the pattern to prevent bundling unused dependencies (e.g., stripping out gRPC if HTTP is used).
- **The Implementation Flow:**
    - The AI must extract the `node.Build(opts Options)` function from `server.go` into a new file named `wire_grpc_etcd.go`.
    - At the absolute top of `wire_grpc_etcd.go` (before the package declaration), the AI must add the exact build constraint: `//go:build grpc && etcd`.
    - This explicitly tells the Go compiler: "Only compile this wiring file if the user explicitly requests gRPC and Etcd."

### Component 2: The Build System (`Makefile`)

*Objective: Orchestrate the generation of optimized Docker images using dynamic build arguments and cross-compilation.*

**2.1. Flow for Makefile Variables and Target Matrix**

- **The Target:** Create a deterministic `Makefile` that translates user profiles into Go build tags.
- **The Implementation Flow:**
    - Define default variables at the top of the `Makefile`: `PROFILE ?= default-etcd`, `GOOS ?= linux`, `GOARCH ?= amd64`.
    - Implement conditional logic: If `PROFILE=default-etcd`, set a `TAGS` variable to `"grpc etcd"`. (If unknown profile, the Makefile MUST exit with an error code and print supported profiles).
    - Create a `docker-build` target. This target must execute:
    `docker build --build-arg TAGS="$(TAGS)" --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(GOARCH) -t sandstore-node:latest -f deploy/docker/Dockerfile .`
    - Create a `clean` target. This target must execute: `rm -rf ./bin && docker compose -f deploy/docker/docker-compose.yaml down -v` to fulfill the "Clean Teardown" user story.

### Component 3: The Container Image (`Dockerfile`)

*Objective: Create a multi-stage Dockerfile that produces a highly optimized, zero-OS-overhead image for the Sand Store node.*

**3.1. Flow for Stage 1: The Builder**

- **The Target:** Compile a static Go binary safely.
- **The Implementation Flow:**
    - Use `golang:1.21-alpine` (or current Go version) as the base.
    - Declare the `ARG`s passed from the Makefile: `TAGS`, `GOOS`, `GOARCH`.
    - **CRITICAL:** Set the environment variable `CGO_ENABLED=0`. *Edge Case Handled:* If this is missing, the binary will dynamically link to Alpine's C-library and crash when moved to the final scratch image.
    - Copy `go.mod` and `go.sum`, run `go mod download`.
    - Copy the source code.
    - Execute the build command exactly as: `go build -tags="${TAGS}" -os="${GOOS}" -arch="${GOARCH}" -o /build/sandstore ./cmd/sandstore`.
    - Execute a second build command for the test client: `go build -o /build/smoke ./clients/client`.

**3.2. Flow for Stage 2: The Runner**

- **The Target:** Package the compiled binaries into a minimal runtime environment.
- **The Implementation Flow:**
    - Use `alpine:latest` as the base image (Using Alpine instead of `scratch` allows for basic `sh` and `curl` debugging for learners).
    - Create the necessary directories: `RUN mkdir -p /var/lib/sandstore/data /var/lib/sandstore/logs`.
    - Copy `/build/sandstore` and `/build/smoke` from the Builder stage to `/usr/local/bin/`.
    - Set the `ENTRYPOINT` to `["/usr/local/bin/sandstore"]`.

### Component 4: Local Orchestration (`docker-compose.yaml`)

*Objective: Wire the Control Plane (Etcd), Data Plane (Nodes), and Test Client into an automated, sequence-aware startup flow.*

**4.1. Flow for the Control Plane (`etcd` & `etcd-init`)**

- **The Target:** Start Etcd and pre-populate it with the 3-node Raft configuration BEFORE any Sand Store nodes boot up.
- **The Implementation Flow:**
    - Define a custom bridge network (e.g., `sandstore-net`) so all containers can resolve each other by name.
    - Define the `etcd` service using `quay.io/coreos/etcd:v3.5.0` (exposing ports 2379 and 2380). Add a `healthcheck` that pings the Etcd endpoint to verify it is alive.
    - Define the `etcd-init` service. This container uses a basic Alpine image with `curl` installed.
    - Mount the local `init-etcd.sh` script into this container.
    - `etcd-init` MUST have a `depends_on` block for `etcd` with `condition: service_healthy`.
    - The command executes `init-etcd.sh` to inject the JSON topology into Etcd, then the container gracefully exits.

**4.2. Flow for the Data Plane (`node-1`, `node-2`, `node-3`)**

- **The Target:** Boot the 3 Sand Store nodes using the custom Docker image.
- **The Implementation Flow:**
    - Define 3 separate services (`node-1`, `node-2`, `node-3`).
    - Each node MUST have a `depends_on` block for `etcd-init` with `condition: service_completed_successfully`. *Edge Case Handled:* This guarantees nodes don't crash loop looking for missing Etcd configuration.
    - Environment Variables: Set `ETCD_ENDPOINTS=etcd:2379`.
    - Command overrides: `-server node --node-id node-X --listen node-X:8080 --data-dir /var/lib/sandstore/data`.
    - Volumes: Mount a local host directory (e.g., `./run/node-X/data:/var/lib/sandstore/data`) for persistence.

**4.3. Flow for the Smoke Test Container**

- **The Target:** Automatically run the tests once the cluster elects a leader.
- **The Implementation Flow:**
    - Define a `smoke-test` service using the same `sandstore-node:latest` image.
    - Override the entrypoint to run a custom shell script (or `sh -c`).
    - **The Synchronization Logic:** The script must run a `while` loop curling the nodes (or checking a health endpoint if you have one, or simply `sleep 10` for the MVP) to wait for Raft leader election.
    - Once the wait period is over, execute `/usr/local/bin/smoke` with the environment variable `SANDSTORE_ADDR=node-1:8080`.

---

## Stack

- Stack is already picked out because the entire project is in Go.