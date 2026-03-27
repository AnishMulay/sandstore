# DX Audit: Hyperconverged Topology — New Engineer Simulation

> Simulation date: 2026-03-27. Branch audited: `dx/hyperconverged`.
> Entry point: README.md, read cold, as a new engineer who found the project via a blog post.

---

## Summary

The project has a compelling README — the pitch is clear, the architecture table is genuinely useful, and the Quick Start *looks* like three commands. Unfortunately the very first thing you're told to run (`./scripts/dev/run-smoke.sh`) does not exist. A new engineer hits a hard wall immediately. The real entry points are `make smoke-local TOPOLOGY=hyperconverged` for local and `make cluster-up / smoke-test / cluster-down` for Kubernetes — but neither path is documented in the README. `docs/` is an empty directory with a `.gitkeep`. `INSTALL.md` describes an old architecture that no longer matches the codebase. The benchmark tool exists but is undocumented and invisible from `make help`. The Kubernetes path silently requires `kubectl` and a running k8s context, neither of which is mentioned in prerequisites. **Overall score: 4/10.** The bones are good; the docs are broken.

---

## Confidence progression

| Phase | Confidence (1–10) | Reason |
|---|---|---|
| After reading README | 5 | Quick Start looks simple, but the lone script path creates a nagging question: is this actually tested? |
| After finding `run-smoke.sh` is missing | 1 | The single documented local path is a dead end. Stuck. |
| After discovering `make smoke-local` via Makefile inspection | 5 | A working path exists; I just had to find it myself. |
| After running smoke test | 7 | Test ran and passed once the correct command was found. |
| After benchmark | 4 | Found `make bench` buried in the Makefile; `make help` doesn't mention it; output only goes to a CSV file. |
| After teardown | 6 | `make cluster-down` is in `make help` for k8s. Local etcd teardown is nowhere documented. |

---

## Friction points log

### FP-001: `./scripts/dev/run-smoke.sh` does not exist

**Phase:** cluster-up
**Severity:** blocker
**What happened:** The README Quick Start, under "Start a 3-node local cluster:", tells the user to run `./scripts/dev/run-smoke.sh`. The file does not exist anywhere in the repository. `scripts/dev/` does not exist at all.
**Exact error or confusion:**
```
bash: ./scripts/dev/run-smoke.sh: No such file or directory
```
**What a new engineer would do:** Open a GitHub issue titled "Quick Start script is missing". Many would just give up here.
**Resolution during simulation:** Inspected the Makefile and found `make smoke-local TOPOLOGY=hyperconverged` which calls the actual script at `scripts/topologies/hyperconverged/smoke-local.sh`. This is the functional equivalent of what `run-smoke.sh` should have done.
**Recommended fix:** Update the README Quick Start to use `make smoke-local TOPOLOGY=hyperconverged`. This command exists, is tested, and is already in `make help`. Alternatively, create `scripts/dev/run-smoke.sh` as a thin wrapper that calls `make smoke-local TOPOLOGY=hyperconverged` — but updating the docs is simpler.

---

### FP-002: Two paths (local vs Kubernetes) are presented without labelling

**Phase:** first impressions
**Severity:** major
**What happened:** The Quick Start section shows a local flow (`./scripts/dev/run-smoke.sh`) immediately followed by a Kubernetes flow (`make test-cluster`) with no separation, no heading, and no explanation of when to use which. A new engineer on a laptop with no k8s context will not know that `make test-cluster` requires Docker Desktop Kubernetes (or kind/minikube) and `kubectl`.
**Exact error or confusion:**
> "**Kubernetes (full integration suite):**
> `make test-cluster`
> Builds Docker images, deploys a 3-node cluster to Kubernetes..."

There is no sentence that says "this requires a running Kubernetes cluster". A developer who has Docker but not kubectl will run this and get `kubectl: command not found`.
**What a new engineer would do:** Try `make test-cluster`, see it fail, wonder if they missed a setup step, re-read the README, find nothing, give up.
**Resolution during simulation:** Read the Makefile to confirm `kubectl` is required.
**Recommended fix:** Split the Quick Start into two clearly labelled sections: "**Local development (no Kubernetes required)**" and "**Integration testing (requires Docker Desktop Kubernetes or kind)**". Add `kubectl` to the prerequisites for the k8s path. Clarify that for most new contributors, the local path is the right starting point.

---

### FP-003: Prerequisites are incomplete for the Kubernetes path

**Phase:** first impressions
**Severity:** major
**What happened:** README prerequisites state: "Go 1.24+, Docker with Compose, Bash, free ports 2379, 2380, 9001–9003". This covers only the local path. The Kubernetes path additionally requires:
- `kubectl`
- A running Kubernetes context (Docker Desktop with k8s enabled, kind, or minikube)
- Sufficient memory to run a 3-node etcd StatefulSet, 3 sandstore pods, and Prometheus

None of this is listed.
**Exact error or confusion:** User follows the prerequisites list, finds it satisfied, runs `make test-cluster`, gets:
```
kubectl is required
```
(from `cluster-up.sh` line 23)
**What a new engineer would do:** Google "kubectl install", install it, then realize they also need a k8s cluster running, spend 30+ minutes configuring Docker Desktop or installing kind.
**Resolution during simulation:** Inferred requirements from reading `cluster-up.sh` source.
**Recommended fix:** Add kubectl + k8s context to prerequisites for the Kubernetes path. Consider adding a version requirement (kubectl v1.26+?) and a note that Docker Desktop's built-in Kubernetes is sufficient.

---

### FP-004: README says cluster stays up; `smoke-local.sh` tears it down

**Phase:** cluster-up / teardown
**Severity:** minor
**What happened:** README says: "The cluster stays up after the script finishes for manual exploration." But `smoke-local.sh` registers a `trap cleanup EXIT` that kills all three node processes when the script exits (whether on success or failure). After `make smoke-local` finishes, no nodes are running.
**Exact error or confusion:** A developer who follows the README, sees the smoke pass, then tries to manually poke the cluster finds nothing listening on ports 9001–9003.
**What a new engineer would do:** Wonder if they did something wrong. Try to reconnect, get connection refused, be confused.
**Resolution during simulation:** Read `smoke-local.sh` source, saw the `trap cleanup EXIT` pattern.
**Recommended fix:** Either (a) update the README to say the local smoke test is self-contained and tears down on completion, and point to `make cluster TOPOLOGY=hyperconverged` for a persistent local cluster; or (b) change `smoke-local.sh` to not kill nodes on clean exit, matching what the README describes.

---

### FP-005: `make bench` is not in `make help` and has no documentation

**Phase:** benchmark
**Severity:** major
**What happened:** `make bench` exists in the Makefile and works, but:
1. It does not appear in `make help` output at all.
2. There is no documentation file explaining what it does, what arguments it takes, or how to interpret the output.
3. `docs/` is empty (see FP-006).
4. The bench binary requires `--seeds` and `--concurrency` flags, which are not demonstrated anywhere in docs.
5. There is no example of what seed addresses look like for the local topology (`127.0.0.1:9001,127.0.0.1:9002,127.0.0.1:9003`).

**Exact error or confusion:** Running `make bench` without flags:
```
--seeds is required
```
Running it correctly (discovered only by reading the Makefile):
```
make bench SEEDS=127.0.0.1:9001,127.0.0.1:9002,127.0.0.1:9003 CONCURRENCY=4
```
Output goes silently to `results/bench/<RFC3339-timestamp>.csv`. The terminal only prints:
```
write benchmark complete
read benchmark complete
stat benchmark complete
create benchmark complete
listdir benchmark complete
all benchmarks complete
```
No numbers. The CSV file is the only place to see P50/P95/P99 latencies.
**What a new engineer would do:** Run `make bench`, fail, search the README for "bench", find nothing, give up or guess at flags.
**Resolution during simulation:** Read `clients/bench/main.go` and the Makefile directly.
**Recommended fix:** (1) Add `make bench` to `make help` with a usage example showing `SEEDS` and `CONCURRENCY`. (2) Create `docs/topologies/hyperconverged/benchmarks.md` documenting the bench workflow end-to-end: start cluster, run bench, find CSV output, interpret numbers. (3) Print a summary table to stdout at the end of the benchmark run so results are immediately visible without opening a CSV.

---

### FP-006: `docs/` directory is empty

**Phase:** first impressions
**Severity:** major
**What happened:** The README repository layout lists `docs/` as "Design documents". The directory contains only a `.gitkeep` file — no documents of any kind.
**Exact error or confusion:**
```
ls docs/
# (empty — just .gitkeep)
```
A new engineer who clicks `docs/` in GitHub hoping to understand the architecture, read a design doc, or find a benchmarking guide finds nothing.
**What a new engineer would do:** Assume docs are not written yet. Lower confidence in the project's maturity. Possibly give up on finding deeper context.
**Resolution during simulation:** Fell back to reading source code directly.
**Recommended fix:** Either (a) remove `docs/` from the repo layout description in the README until content exists, or (b) create at minimum `docs/topologies/hyperconverged/README.md` and `docs/topologies/hyperconverged/benchmarks.md`. The benchmark doc is immediately needed for the benchmark phase; the topology overview would help new contributors enormously.

---

### FP-007: `INSTALL.md` describes an old architecture that no longer matches the codebase

**Phase:** cluster-up (if following CONTRIBUTING.md → INSTALL.md)
**Severity:** major
**What happened:** `CONTRIBUTING.md` tells new engineers to "Follow the installation guide: Complete the setup in [INSTALL.md](INSTALL.md)" as their first development step. `INSTALL.md` describes an architecture that has been replaced:

- References `servers/simple/` and `servers/raft/` — these directories do not exist
- References `cmd/client/` and `cmd/mcp/` as server entry points — wrong paths
- Shows build commands: `go build -o bin/sandstore-client ./cmd/client` — `cmd/client` doesn't exist
- Shows `--server raft` flag — the actual flag is `--server node`
- Troubleshooting section references ports 8080–8082 with no mention of 9001–9003
- Directory structure shown is completely different from the actual repo

**Exact error or confusion:** Following `INSTALL.md` step 4:
```bash
go build -o bin/sandstore-client ./cmd/client
# Error: cannot find main module; see 'go help modules'
# (or: pattern ./cmd/client: directory prefix cmd/client does not contain main module)
```
**What a new engineer would do:** Assume they cloned wrong, or that the repo is broken.
**Resolution during simulation:** Ignored INSTALL.md and used the Makefile instead.
**Recommended fix:** Rewrite INSTALL.md to reflect the current hyperconverged architecture. At minimum: correct the directory structure diagram, correct the build commands (`make build`, `make client`), correct the flag examples (`--server node`), and replace the "Running Individual Components" section with the `make cluster-up / smoke-local / cluster-down` workflow.

---

### FP-008: `make cluster` (via `run-local.sh`) starts nodes without checking etcd

**Phase:** cluster-up
**Severity:** minor
**What happened:** `make cluster TOPOLOGY=hyperconverged` calls `run-local.sh`, which builds and starts three nodes but does **not** check whether etcd is reachable first. If etcd is not running, the nodes start, log to `run/cluster/*/stdout.log`, and fail silently at cluster membership lookup. The only indication of failure is in log files that the user hasn't been told to look at.

By contrast, `smoke-local.sh` does check for etcd at the top (`require_etcd`) and gives a clear error with the remedy command.
**Exact error or confusion:** User runs `make cluster TOPOLOGY=hyperconverged` without starting etcd. All three nodes appear to start (PIDs are printed), then the user runs `make client` and gets connection refused. Log file contains:
```
failed to connect to etcd: context deadline exceeded
```
but no terminal output indicates anything went wrong.
**What a new engineer would do:** Assume the cluster started, spend time debugging why the client can't connect, eventually find the log files.
**Resolution during simulation:** Noticed `require_etcd` in `smoke-local.sh` by comparison; verified etcd was running.
**Recommended fix:** Add the same `require_etcd` check from `smoke-local.sh` to `run-local.sh` (3 lines). Also, print a hint at the top of `run-local.sh` output: "Requires etcd on localhost:2379. Start with: docker compose -f deploy/docker/etcd/docker-compose.yaml up -d".

---

### FP-009: No teardown instruction for local etcd

**Phase:** teardown
**Severity:** minor
**What happened:** After the smoke test completes (successfully), etcd is still running in Docker. There is no instruction anywhere in the README, Quick Start, or `make help` on how to tear down etcd after a local run.

`make cluster-down` tears down the Kubernetes namespace, not local Docker containers.
`make clean-runtime` does tear down Docker compose stacks (including etcd), but is never mentioned in the local workflow docs.
**Exact error or confusion:** After `make smoke-local` exits, if you run `docker ps` you see `sandstore-etcd` still running. Ports 2379 and 2380 are still bound. If you try to run the quick start again in a fresh session, etcd starts fine (already running or compose up is idempotent), but it's never clear the old etcd was still there.
**What a new engineer would do:** Leave etcd running forever (many developers will). Or get confused if something else needs port 2379.
**Resolution during simulation:** Identified `docker compose -f deploy/docker/etcd/docker-compose.yaml down` as the teardown command.
**Recommended fix:** Add a "Teardown" section to the Quick Start (even a one-liner): "When done: `docker compose -f deploy/docker/etcd/docker-compose.yaml down`". Or add a `make etcd-down` target and mention it.

---

### FP-010: `scripts/bench/` is empty — implies work planned but absent

**Phase:** benchmark
**Severity:** minor
**What happened:** `scripts/bench/` exists with only a `.gitkeep`. The CONTRIBUTING.md roadmap lists "Performance benchmarking suite" as a High Priority item. A new engineer looking for benchmark scripts will find an empty directory. Combined with FP-005 (no bench docs) and FP-006 (no docs at all), this creates the impression that benchmarking is aspirational but not actually usable — when in fact the `make bench` target works.
**Exact error or confusion:** Looking for benchmark scripts: `ls scripts/bench/` → empty.
**What a new engineer would do:** Conclude benchmarking is not implemented yet, skip the benchmark phase entirely.
**Resolution during simulation:** Found `make bench` in the Makefile by reading all targets.
**Recommended fix:** Either add a `scripts/bench/bench-local.sh` wrapper that calls `make bench` with sensible defaults and prints the output CSV path, or remove the empty `scripts/bench/` directory to avoid implying placeholder content.

---

### FP-011: Benchmark output is CSV-only; no terminal summary

**Phase:** benchmark
**Severity:** minor
**What happened:** After `make bench` completes (all benchmarks run for `DURATION` seconds each), the terminal only shows:
```
write benchmark complete
read benchmark complete
stat benchmark complete
create benchmark complete
listdir benchmark complete
all benchmarks complete
```
The actual P50/P95/P99 latency numbers are in `results/bench/<RFC3339-timestamp>.csv`. There is no indication of where this file is, and no terminal summary of the results.
**Exact error or confusion:** New engineer runs the benchmark, sees "all benchmarks complete", and has no idea what the numbers were or where to find them.
**What a new engineer would do:** Think the benchmark produced no output. Re-run with `-v` flags that don't exist. Eventually discover CSV by running `ls results/`.
**Resolution during simulation:** Read the bench source code to find the output path.
**Recommended fix:** At the end of `main()` in `clients/bench/main.go`, print the CSV path and a formatted summary table to stdout. Even a simple `fmt.Printf("Results written to: %s\n", outputPath)` would help.

---

## What worked well

- **The one-liner project description** in the README is excellent: "A modular framework for building and experimenting with distributed storage architectures." Clear, accurate, immediately useful.
- **The architecture table** (layer, interface, active implementation) is the best part of the README. New engineers who work through the confusion will find it very valuable.
- **`make help` output** is clean and shows TOPOLOGY-parameterized usage examples. The pattern `make <target> TOPOLOGY=hyperconverged` is intuitive once discovered.
- **`smoke-local.sh` is well-written** — it checks prerequisites (`require_etcd`), builds binaries, bootstraps etcd config, starts nodes, polls for leader election, runs the smoke, and cleans up. The approach is solid; it just isn't reachable from the documented path.
- **`init-etcd.sh`** is clear and well-commented.
- **`cluster-down.sh`** properly waits for namespace deletion and persistent volume cleanup — no fire-and-forget teardown.
- **Port conflict detection** in `smoke-local.sh` (ports 9001/9002/9003 check before start) prevents confusing failures from stale processes.
- **Error message in `cluster-up.sh`** when etcd is missing: provides the exact remediation command. More scripts should do this.
- **`make clean-runtime`** is comprehensive: kills processes, tears down all compose stacks, checks for lingering ports. The problem is that it's never mentioned in any workflow doc.

---

## Prioritized fix list

| Priority | Friction Point | Fix Type | Effort |
|---|---|---|---|
| 1 | FP-001: `run-smoke.sh` missing — Quick Start is broken | doc | small |
| 2 | FP-007: `INSTALL.md` describes wrong architecture | doc | medium |
| 3 | FP-002: Two paths unlabelled — local vs Kubernetes | doc | small |
| 4 | FP-003: kubectl missing from prerequisites | doc | small |
| 5 | FP-005: `make bench` undocumented and missing from `make help` | both | small |
| 6 | FP-006: `docs/` directory is empty | doc | large |
| 7 | FP-004: README claims cluster stays up; it doesn't | doc | small |
| 8 | FP-008: `run-local.sh` starts nodes without checking etcd | code | small |
| 9 | FP-009: No etcd teardown instruction for local path | doc | small |
| 10 | FP-011: Benchmark results go to CSV only, no terminal summary | code | small |
| 11 | FP-010: `scripts/bench/` is an empty placeholder | both | small |

---

## Appendix: Actual working local workflow (as discovered, not as documented)

```bash
# 1. Clone
git clone https://github.com/AnishMulay/sandstore
cd sandstore

# 2. Start etcd
docker compose -f deploy/docker/etcd/docker-compose.yaml up -d

# 3. Run cluster + smoke test (builds, starts 3 nodes, elects leader, tests, tears down)
make smoke-local TOPOLOGY=hyperconverged

# 4. For persistent cluster (manual exploration):
make cluster TOPOLOGY=hyperconverged    # NOTE: etcd must be running first; this doesn't check

# 5. Run benchmark against a running local cluster
make bench SEEDS=127.0.0.1:9001,127.0.0.1:9002,127.0.0.1:9003 CONCURRENCY=4
# Results in: results/bench/<timestamp>.csv

# 6. Teardown etcd (not documented anywhere)
docker compose -f deploy/docker/etcd/docker-compose.yaml down
```

## Appendix: Actual working Kubernetes workflow (as discovered, not as documented)

```bash
# Prerequisites (undocumented): kubectl, Docker Desktop with Kubernetes enabled

# 1. Build images and deploy to Kubernetes
make cluster-up TOPOLOGY=hyperconverged

# 2. Run smoke test as a Kubernetes Job
make smoke-test TOPOLOGY=hyperconverged

# 3. Tear down
make cluster-down TOPOLOGY=hyperconverged
```
