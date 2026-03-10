# March 9, 2026 State

This document is intentionally narrower than a full audit.

Its purpose is to answer one question:

> Which issues are serious enough to discredit a Sandstore vision paper / benchmarking effort if left unfixed?

I am excluding smaller issues, polish work, and "nice to have" distributed systems improvements. This is the minimum set of blockers that materially affect the credibility of:

- durability claims
- crash-consistency claims
- routing claims
- benchmark-defensibility

## Executive Summary

If the immediate goal is a vision paper plus early benchmark data, there are **4 major blockers**:

1. **Raft/WAL durability is not yet strong enough to support hard durability claims.**
2. **The 2PC write path returns success before chunk finalization is proven complete.**
3. **Crash recovery for prepared chunk files is not wired up at node startup.**
4. **The durability smoke harness does not yet prove acknowledged-write survival or interrupted-rename correctness.**

If those 4 are fixed, the project becomes much more defensible for a vision paper and for early benchmark discussion.

The direct-client routing issue is real, but it is lower priority **for a vision paper** because the server-side path already performs deterministic direct replica access. It matters more if the paper explicitly claims a smart client that bypasses the leader for reads.

## What Would Actually Discredit the Paper

These are the issues that can directly invalidate a central claim:

- claiming "durable Raft replication" without durable WAL semantics
- claiming "crash consistency" while acknowledged writes can still be lost or stranded
- claiming "2PC intent safety" without reboot reconciliation
- presenting benchmark numbers before the test harness proves crash outcomes

These are the issues I would fix before publishing benchmark-backed claims.

## Priority Blockers

### 1. Raft/WAL durability is not yet benchmark-defensible

**Why it matters**

If the paper claims that Raft replication pays the real fsync penalty and that committed metadata is durable across crashes, this must be true in the WAL path. Right now the implementation is close, but not strict enough.

**Why it is a blocker**

- follower `AppendEntries` success depends on `StoreLogs()`
- `StoreLogs()` does `f.Sync()`
- but rename durability is still effectively best-effort because directory sync failures are ignored
- the WAL also lacks corruption/torn-write framing such as CRC + length/trailer

That means a hostile reviewer can argue that the system is still relying on filesystem behavior more than on a fully explicit durability protocol.

**Repository evidence**

- `internal/metadata_replicator/durable_raft/stores.go`
- `internal/metadata_replicator/durable_raft/replicator.go`

**Classification**

- **Needs a design doc:** Yes

**Why this needs a design doc**

This is not just a local patch. You need to decide:

- whether to keep the current whole-file JSON WAL or move to record-oriented WAL
- what corruption model you are defending against
- what "durable append acknowledged" means precisely
- what the recovery contract is for partial writes, rename loss, and snapshot interplay

This affects benchmark positioning and the architecture story, not just code.

**Suggested scope of the design doc**

- WAL format
- fsync rules
- recovery behavior
- checksum/corruption policy
- snapshot/WAL interaction

### 2. 2PC commit returns before physical chunk finalization is confirmed

**Why it matters**

This is the biggest correctness gap in the write path. The metadata intent is replicated first, but the chunk commit RPCs are sent asynchronously afterward. That means the API can report success even though the physical chunk rename has not yet completed everywhere.

**Why it is a blocker**

If you claim that an acknowledged write survives crashes intact, this path undercuts that claim. A committed metadata intent with unfinished chunk finalization is exactly the kind of bug crash-consistency tools are designed to expose.

**Repository evidence**

- `internal/orchestrators/raft_tx_coordinator.go`
- `internal/chunk_service/local_disc/local_disc_posix_chunk_service.go`

**Classification**

- **Needs a design doc:** Yes

**Why this needs a design doc**

You need an explicit product decision about what success means:

- success after metadata commit only
- success after all participants finalize
- success after quorum finalize
- lazy finalization on read/restart as part of the contract

That is a protocol decision, not just an implementation cleanup.

**Suggested scope of the design doc**

- write acknowledgment semantics
- participant commit requirements
- recovery contract for committed-intent / unfinalized-chunk states
- timeout and retry behavior

### 3. Prepared chunk recovery is not wired into startup

**Why it matters**

Even if the lazy-finalization logic is acceptable, it currently is not operational at startup because the chunk service is not fully wired:

- the chunk service startup scan is not called
- the metadata service is not injected into the chunk service

So reboot recovery is not actually complete.

**Why it is a blocker**

This weakens both the architecture story and the crash-consistency story. You cannot credibly say the system safely recovers orphaned or prepared chunk states after crashes if the reboot reconciliation path is not activated in the running node.

**Repository evidence**

- `servers/node/wire_grpc_etcd.go`
- `internal/chunk_service/local_disc/local_disc_posix_chunk_service.go`

**Classification**

- **Can be solved directly with AI / focused implementation:** Yes

**Why this does not need its own design doc**

The design intent already exists in code:

- temp `.tmp` staging files
- prepared index rebuild
- intent-state lookup
- lazy finalize / purge behavior

The missing work is mostly integration and operational wiring, not a new protocol decision.

**Expected implementation scope**

- call `cs.SetMetadataService(ms)`
- call `cs.Start()` during node startup
- ensure shutdown/startup lifecycle is clean
- verify restart behavior with a targeted crash test

### 4. Crash-consistency harness does not test the claims you want to make

**Why it matters**

Right now the durability smoke suite mostly exercises cluster restart, snapshot catch-up, and interrupted create visibility. It does **not** prove:

- acknowledged data writes survive crash and reboot with correct bytes
- interrupted rename leaves the namespace valid

**Why it is a blocker**

Without those tests, benchmark numbers and durability claims are much easier to attack. A reviewer can reasonably say: "You measured throughput before proving the write path survives crash conditions."

**Repository evidence**

- `clients/durability_smoke/main.go`

**Classification**

- **Can be solved directly with AI / focused implementation:** Yes

**Why this does not need its own design doc**

The missing requirement is already clear:

- `Call -> Kill -> Assert`

The work is test engineering, not architecture invention.

**Expected implementation scope**

- add acknowledged write crash test
- add interrupted rename crash test
- add verification of exact bytes after reboot
- add verification that namespace converges to a valid post-crash state

## Important But Not Mandatory For The First Vision Paper

These matter, but I would not let them block a first vision paper unless you explicitly overclaim them.

### Smart client direct-to-chunk-owner routing

**Current state**

- the client still sends reads and writes to the server leader/router
- the server then performs deterministic direct access to the physical chunk owners

**Why this is not a top blocker right now**

If the paper says:

> Sandstore uses deterministic O(1) placement metadata and avoids scatter-search in the data path

then the current implementation is directionally defensible.

If the paper says:

> the smart client directly dials physical chunk holders for reads

then it is not yet true and becomes a blocker.

**Classification**

- **Needs a design doc:** No, unless the paper wants to make the stronger smart-client claim
- **Can be solved directly with AI / focused implementation:** Likely yes

### Legacy chunk-write bypass path

There is still a legacy chunk-write entry point that bypasses the proper metadata-intent path.

I do not think this alone discredits the paper if:

- it is unused in the benchmark setup
- the paper clearly scopes claims to the orchestrated path

But for benchmark hygiene, it should be disabled or clearly excluded.

**Classification**

- **Can be solved directly with AI / focused implementation:** Yes

## Recommended Work Order

If you want the highest return on effort for a vision paper:

1. **Fix startup recovery wiring for prepared chunks.**
2. **Upgrade the durability smoke harness to prove acknowledged-write and rename crash behavior.**
3. **Decide and document the 2PC acknowledgment contract.**
4. **Decide and document the WAL durability/corruption contract.**

That sequence gives you:

- a functioning recovery story quickly
- executable proof for crash behavior
- then architectural rigor for the two hardest protocol questions

## Final Recommendation

For your stated goal, I would split the major blockers like this.

### Needs a proper software design doc

- **WAL durability contract**
- **2PC write acknowledgment and recovery contract**

These define the truthfulness of the paper's core claims.

### Safe to solve directly with AI / targeted implementation

- **startup wiring for prepared chunk recovery**
- **crash-consistency test harness upgrades**
- **disable or quarantine the legacy chunk-write bypass**

## Practical Paper Positioning

If you want to publish sooner, the safest positioning is:

- describe Sandstore as a **hyperconverged research prototype**
- explicitly say the current system is being hardened for full crash-consistency validation
- avoid making strong claims about smart-client direct reads unless you implement them
- avoid saying "production-grade durability" until the WAL and 2PC contracts are formally tightened

That framing keeps the paper honest while still letting you present the architectural direction and early benchmark motivation.
