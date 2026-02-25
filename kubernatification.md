# Kubernatification

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

**1. Remote Orchestration & Zero-Touch Deployment**

- **Primary Goal:** Achieve a "single-command" deployment from your MacBook to the remote Linux node.
- **Success Metric:** `kubectl apply -f manifests/` (or a Helm install) from your local machine successfully brings up the Etcd control plane and the 3-node Raft data plane on the laptop.

**2. Dynamic Service Discovery in K8s**

- **Primary Goal:** Transition from Docker Compose’s static/DNS-based discovery to Kubernetes native discovery.
- **Technical Detail:** Ensure the Raft nodes can utilize K8s Headless Services or Etcd-backed discovery to identify peers across the pod network without manual IP configuration.

**3. Hardware-Aware Scheduling (Preparation)**

- **Primary Goal:** Implement Node/Pod affinity and Taints/Tolerations to ensure storage-heavy data plane nodes land specifically on the SSD-backed hardware.
- **Success Metric:** Define a `StorageClass` or specific labels that the Sand Store data nodes respect during scheduling.

**4. Observability and Performance Parity**

- **Primary Goal:** Ensure the "open_smoke" testing client can reach the cluster from outside (NodePort/LoadBalancer) to measure latency.
- **Success Metric:** Baseline performance on K8s should match or exceed the Docker Compose setup, accounting for CNI (Container Network Interface) overhead.

---

## User stories

### Persona: The Developer / Operator

*Context: Working from a MacBook, controlling a single Kubernetes cluster running on a remote old gaming laptop to prepare for hardware-aware storage benchmarking.*

### 1. Build & Network Handoff (Getting code to the cluster)

- **Story 1.1 (Happy Path - Build & Push):** As a developer, I want to run `make k8s-build PROFILE=<name>`, which compiles my Go code using the correct build tags, packages it into a minimal Docker image, and pushes it to a container registry (e.g., Docker Hub), so the remote Kubernetes cluster has a place to download the new binaries.
- **Story 1.2 (Sad Path - Pre-flight Failure):** As a developer, if my Go code fails to compile, or if I am not logged into the container registry, I want the `Makefile` pipeline to fail instantly with a clear error *before* it attempts to run any `kubectl` commands, ensuring my existing running cluster isn't touched by a broken build.

### 2. Cluster Bootstrapping & Hardware Targeting

- **Story 2.1 (Happy Path - The Deploy):** As an operator, I want to run `make k8s-deploy PROFILE=<name>`. This should apply all necessary K8s manifests (via `kubectl apply`) to my remote laptop, bringing up the Etcd control plane and the 3-node Sand Store data plane using the image I just built.
- **Story 2.2 (Happy Path - Hardware Awareness):** As an operator preparing for benchmarks, I want my deployment manifests to utilize a specific K8s `StorageClass` or `NodeSelector`, ensuring that my Sand Store data nodes are explicitly allocated storage on the laptop's physical SSD rather than a slower drive.
- **Story 2.3 (Edge Case - Startup Race Conditions):** As an operator, if the Sand Store nodes boot up faster than the Etcd control plane, I want the pods to gracefully retry their connection (or be restarted by K8s) until Etcd is ready, mimicking the resilient startup we had in Docker Compose.
- **Story 2.4 (Sad Path - Insufficient Hardware):** As an operator, if I accidentally deploy a configuration that requests more CPU, RAM, or SSD storage than my laptop physically has, I want Kubernetes to place the pods in a `Pending` state with clear "Insufficient Resources" events, rather than crashing the laptop's OS.

### 3. The Iteration Loop (Updating code)

- **Story 3.1 (Happy Path - Rolling Updates):** As a developer, when I write a new feature or bug fix, I want to run `make k8s-update PROFILE=<name>`. This must push the new image and trigger a Kubernetes rollout. The system should replace the 3 Raft nodes *one at a time*, ensuring the cluster maintains a quorum and doesn't experience total downtime.
- **Story 3.2 (Sad Path - Bad Code Halts Rollout):** As an operator, if I deploy new code that contains a fatal Go panic, I want Kubernetes to detect the crashing pod (CrashLoopBackOff) and *halt the rolling update process automatically*, preventing the bad code from taking down the remaining healthy Raft nodes.

### 4. Observability & Debugging

- **Story 4.1 (Happy Path - Log Aggregation):** As a developer, I want a single command (e.g., `make k8s-logs`) that streams and multiplexes the logs of all 3 Sand Store pods directly to my MacBook terminal, so I can watch Raft elections happen in real-time.
- **Story 4.2 (Edge Case - Post-Mortem Debugging):** As an operator, if a Sand Store pod silently crashes and restarts in the middle of the night, I want to be able to use a standard command (like `kubectl logs --previous`) to retrieve the exact logs/panic trace of the *dead* container, rather than only seeing the logs of the newly restarted container.

### 5. Absolute State Teardown (The "One-at-a-Time" Guarantee)

- **Story 5.1 (Happy Path - The Clean Slate):** As an experimenter switching between benchmark profiles, I want to run `make k8s-destroy`. This command must completely delete the pods, the services, and *crucially* delete the `PersistentVolumeClaims` (PVCs), ensuring that the underlying SSD is completely wiped of old Raft state and chunks before I start my next test.

---

## Data models

In a Kubernetes architecture, data models do not represent database schemas; rather, they are the declarative YAML manifests that define the desired state of the system's compute, storage, and networking. Because Sand Store is a stateful distributed file system relying on Raft consensus, we cannot treat our nodes as disposable, identical clones. We will rely on four core Kubernetes models to translate our Docker Compose setup into a resilient cluster.

The first model is the StatefulSet, which will handle our compute and identity. Unlike a standard K8s Deployment, a StatefulSet guarantees strict, sticky identities for our pods (such as sandstore-0, sandstore-1, and sandstore-2). This is critical for Raft, as nodes need to know exactly who their peers are, and if a pod crashes, it must come back up with the exact same name and network identity.

To handle our storage and enable our hardware-aware benchmarking goals, we will use a combination of StorageClass and PersistentVolumeClaims (PVCs). Instead of binding local directories as we did in Docker, we will define a StorageClass that specifically targets your laptop's SSD. The StatefulSet will then use a PVC to request a dedicated slice of that SSD for each specific Raft node. This abstraction allows us to easily wipe the disks between tests or swap to an HDD StorageClass later without changing the application code.

For networking and peer discovery, we will implement a Headless Service. Standard K8s services act as load balancers, which breaks Raft since nodes need to communicate directly with specific peers for elections and replication. A Headless Service bypasses load balancing and creates direct DNS records for each individual pod, replacing the static Docker Compose network and allowing our nodes to discover each other dynamically.

Finally, we will use ConfigMaps to manage our environment variables. Instead of hardcoding values like Etcd endpoints or Raft timeouts into our pod definitions, we will extract them into a ConfigMap. This decouples our configuration from our architecture, allowing the Makefile pipeline to easily swap out settings based on the profile we are testing.

---

## MVP

In many software projects, defining an MVP involves aggressively cutting features to reach the fastest possible release. However, for an infrastructure migration like Sand Store's Kubernatification, our defined user stories already represent the absolute minimum baseline required for a functional, testable distributed system. We cannot reduce this scope further without defeating the purpose of the project. Without the automated build pipeline, we cannot deliver code to the remote laptop. Without the StatefulSet and Headless Service, the Raft nodes cannot discover each other to establish a quorum. Furthermore, without the strict storage abstractions and the clean-teardown guarantees, we would lose the reproducible, hardware-aware benchmarking capabilities that motivated this migration in the first place. Therefore, the five core user story workflows we have outlined strictly define our v1.0 MVP, and all must be implemented to consider this phase a success.

---

## Project future

The immediate future of Sand Store involves rigorous, multi-layered benchmarking enabled by this Kubernetes migration. The first phase of this testing will focus on hardware and OS-level storage substrates. By deploying to a dedicated 1TB SSD on a remote node, we can benchmark standard sequential-optimized file systems, like ext4, against flash-native file systems designed specifically for solid-state drives. This will allow us to observe how the underlying Linux storage substrate directly impacts the performance of our distributed Raft data plane.

Beyond hardware benchmarking, the future of Sand Store lies in workload-specific optimizations, particularly for artificial intelligence. Inspired by systems like NVIDIA's AIStore, we want to explore how Sand Store can serve as a highly efficient backend for AI agents and LLMs. Because Sand Store is built with a modular, swappable architecture, we intend to design and benchmark specific component configurations tuned specifically for the high-throughput, read-heavy data access patterns required by modern machine learning workloads.

Ultimately, the long-term vision is plug-and-play modularity. The foundational work of Dockerization and this current Kubernatification phase are integral to making Sand Store universally accessible. In the future, any developer should be able to define a highly specific, custom configuration of Sand Store—whether that is an in-memory chain-replicated cache or a flash-optimized Raft cluster—and effortlessly deploy it to any environment, from an edge device to a managed cloud cluster, with a single command.

---

## Specific components

To ensure zero variability during implementation, the Kubernatification is divided into strictly defined structural components (the YAML manifests) and operational flows (the Makefile pipeline).

### Core Structural Components to be Generated

Before the flows can execute, the following Kubernetes YAML manifests must be defined in a `deploy/k8s/` directory. The AI must strictly use these exact primitives:

1. `configmap.yaml`: Must contain all environment variables (Etcd endpoints, Raft timeouts). Keys must be dynamically overridable by the Makefile `PROFILE` variable.
2. `storageclass.yaml`: Must define a K8s `StorageClass` (named `sandstore-local-ssd`) utilizing the cluster's default `local-path` provisioner. Since the target node operates entirely on a single 1TB SSD, standard dynamic provisioning is sufficient, provided the Teardown flow guarantees strict PVC deletion between benchmark runs.
3. `service-headless.yaml`: Must define a K8s `Service` with `clusterIP: None` and a selector matching the Sand Store pods.
4. **`statefulset-etcd.yaml`**: Must define a 1-replica `StatefulSet` for the control plane.
5. `statefulset-sandstore.yaml`: Must define a 3-replica `StatefulSet` for the data plane. It MUST contain a `volumeClaimTemplates` block requesting storage strictly from the `sandstore-local-ssd` StorageClass. It MUST use the headless service name for its `serviceName` field to generate predictable DNS (e.g., `sandstore-0.headless-svc...`).
6. `service-nodeport.yaml`: Must define a K8s `Service` of type `NodePort` that maps the external port of the laptop to the internal client-facing port of the Sand Store Raft leader. This is strictly required so the `open_smoke` testing client running on the MacBook can route requests across the local Wi-Fi network to the cluster running on the gaming laptop.

**Global Makefile Constraints (Context Safety)**

- **The Rule:** Every single `kubectl` command executed in any Flow below MUST be appended with `-context=$(KUBE_CONTEXT)`.
- **The Variable:** The Makefile must enforce that `KUBE_CONTEXT` is defined (e.g., `KUBE_CONTEXT ?= minikube-laptop`). If the user accidentally runs `make k8s-destroy` while their global kubeconfig is pointing to a production cloud cluster, this strict override guarantees the command will either fail harmlessly or only affect the remote gaming laptop.

### Flow 1: Image Build & Registry Push

- **Trigger:** `make k8s-build PROFILE=<name>`
- **Pre-flight Checks:**
    - Check if Docker daemon is running.
    - Check if the user is authenticated to the target container registry (e.g., `docker login`).
- **Execution Steps:**
    1. Execute `docker build` targeting the existing Sand Store multi-stage Dockerfile. **CRITICAL:** The command must explicitly include `-platform linux/amd64` to ensure the Mac Docker daemon pulls an AMD64 base image.
    2. Pass the `PROFILE` variable into the build command as a Go build tag, and reuse the existing cross-compilation flags from the Dockerization phase (`-build-arg GO_FLAGS="-tags ${PROFILE}" --build-arg GOOS=linux --build-arg GOARCH=amd64`).
    3. Tag the resulting image explicitly as `<registry_url>/sandstore-node:${PROFILE}-latest`.
    4. Execute `docker push <registry_url>/sandstore-node:${PROFILE}-latest`.
- **Expected State:** Exit code 0. Image exists in the remote registry with the updated SHA.
- **Failure Handler:** If build or push fails, exit immediately with a non-zero code. DO NOT attempt to interact with the K8s cluster.

### Flow 2: Absolute Teardown (Clean Slate Guarantee)

- **Trigger:** `make k8s-destroy PROFILE=<name>`
- **Pre-flight Checks:** Verify `kubectl` context is pointing to the remote gaming laptop, NOT a local Docker-desktop cluster.
- **Execution Steps:**
    1. Execute `kubectl delete -f deploy/k8s/ --ignore-not-found=true` to delete all compute and networking resources.
    2. **CRITICAL STEP:** Execute `kubectl delete pvc -l app=sandstore --ignore-not-found=true`. (StatefulSets do not delete PVCs by default; this manual step is mandatory to guarantee a wiped SSD for benchmarking).
    3. Execute `kubectl wait --for=delete pod -l app=sandstore --timeout=60s` to block until teardown is complete.
- **Expected State:** No pods, services, or PVCs belonging to Sand Store exist in the cluster.
- **Failure Handler:** If deletion hangs, output instructions to force-delete terminating namespaces/pods.

### Flow 3: Cluster Bootstrapping & Hardware Targeting

- **Trigger:** `make k8s-deploy PROFILE=<name>`
- **Pre-flight Checks:** Run **Flow 2 (Absolute Teardown)** implicitly. The cluster must be completely empty of Sand Store resources before deploying to guarantee hardware isolation.
- **Execution Steps:**
    1. Apply the base networking and config: `kubectl apply -f deploy/k8s/configmap.yaml -f deploy/k8s/storageclass.yaml -f deploy/k8s/service-headless.yaml`.
    2. Apply the control plane: `kubectl apply -f deploy/k8s/statefulset-etcd.yaml`.
    3. Wait for control plane: `kubectl rollout status statefulset/etcd-cluster --timeout=60s`.
    4. Apply the data plane: `kubectl apply -f deploy/k8s/statefulset-sandstore.yaml`. (Ensure the manifest specifies `imagePullPolicy: Always` so it grabs the newly built image).
- **Expected State:** `kubectl get pods` shows 1 etcd pod and 3 sandstore pods in `Running` state. `kubectl get pvc` shows 3 Bound volumes attached to the `sandstore-local-ssd` StorageClass.
- **Failure Handler:** If pods remain in `Pending`, output a warning to check if the remote laptop's SSD has sufficient capacity based on the PVC requests.

### Flow 4: The Iteration Loop (Stateful Rolling Update)

- **Trigger:** `make k8s-update PROFILE=<name>`
- **Pre-flight Checks:** Verify the cluster is currently running.
- **Execution Steps:**
    1. Run **Flow 1 (Build & Push)** implicitly to get the new code into the registry.
    2. Trigger the K8s rollout: `kubectl rollout restart statefulset/sandstore`.
    3. Block and watch the rollout: `kubectl rollout status statefulset/sandstore --timeout=120s`.
- **Expected State:** Kubernetes terminates `sandstore-2`, brings it back up with the new image, waits for it to become `Ready` (Raft quorum restored), and then proceeds to `sandstore-1`, and finally `sandstore-0`.
- **Failure Handler:** If `kubectl rollout status` times out or fails (due to a Go panic causing a `CrashLoopBackOff`), automatically pause the rollout and instruct the user to run Flow 5 for debugging.

### Flow 5: Observability & Post-Mortem Debugging

- **Trigger:** `make k8s-logs PROFILE=<name>`
- **Execution Steps (Happy Path):** 1. Execute `kubectl logs -l app=sandstore -f --max-log-requests=10`. This strictly multiplexes the logs of all currently running Raft nodes to the MacBook terminal.
- **Trigger (Sad Path / Crash Debugging):** `make k8s-logs-crash POD=<pod_name>`
- **Execution Steps (Sad Path):**
    1. Execute `kubectl logs <pod_name> --previous`. This strictly pulls the panic trace of the *dead* container right before it restarted, rather than the fresh logs of the rebooted container.

---

## Stack

- There isn't a particular choice for using this tag. The entire project is written in Go, so this will also be in Go.