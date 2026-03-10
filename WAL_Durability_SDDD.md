# Zero-Variability Software Design Document
# WAL Durability Contract for Sandstore Durable Raft

**Status:** Draft  
**Companion blocker:** `9_March_2026_state.md` — Blocker #1

---

## Authoritative File List

Exactly three files are modified by this SDDD. No other file is touched.

| File | Nature of change |
|------|-----------------|
| `internal/metadata_replicator/durable_raft/stores.go` | Primary implementation change |
| `internal/metadata_replicator/durable_raft/models.go` | Contract comment update only |
| `servers/node/wire_grpc_etcd.go` | One call-site update for the new `NewFileStableStore` error return |

Every other file in the repository is out of scope. If an edit is not listed in this table, it must not be made.

---

## 1. Goals

The specific objectives of this SDDD are:

1. **Directory fsync must not be silently ignored.** `writeFileAtomically` currently calls `_ = d.Sync()`, discarding the error. This must be changed so that a directory fsync failure is returned to the caller and propagates to `StoreLogs`, `SetState`, and `SaveSnapshot` as an error.

2. **Torn writes must be detectable.** `persistLocked` currently writes raw JSON. After this change it must wrap the payload in a `walEnvelope` struct containing a CRC32 checksum computed over the raw inner JSON bytes. A crash mid-write that produces a file whose CRC does not match must be detected at load time.

3. **WAL load must return a typed error on corruption.** `load()` must return `ErrWALCorrupt` when the CRC does not match, rather than a generic unmarshal error. This typed error propagates through `NewFileLogStore` to the existing `if err != nil { panic(err) }` in `wire_grpc_etcd.go`.

4. **StableStore writes must also be CRC-protected.** `SetState` must wrap its payload in a `stableEnvelope` with a CRC32 field. `GetState` must return `ErrStableCorrupt` on a mismatch.

5. **`NewFileStableStore` must return an error.** It currently returns only `*FileStableStore`. It must be changed to return `(*FileStableStore, error)` so that a corrupt stable file is detected at startup rather than deferred to the first `GetState` call inside the Raft loop. The single call site in `wire_grpc_etcd.go` must be updated to handle this error return.

6. **The `LogStore` interface comment must reflect the full durability contract.** The comment on `StoreLogs` in `models.go` must be updated to explicitly state the directory fsync obligation.

What this SDDD explicitly does **not** change:

- The WAL format stays as a whole-file JSON rewrite. No record-oriented append format is introduced.
- The `RaftMetadataReplicator` in `internal/metadata_replicator/raft_replicator/` is not touched.
- The snapshot store, bolt metadata service, and chunk service are not touched.
- No file outside the authoritative file list above is modified.

---

## 2. User Stories

### US-1 — Durable append acknowledged

As a Raft follower, when `StoreLogs` returns `nil`, the entries must be on stable storage and must survive a hard power cut, so that the node can safely reply `Success: true` to the leader's AppendEntries RPC.

**Acceptance test** (exact — see Section 7.2, `TestStoreLogs_SurvivesReload`):
1. Create a `FileLogStore` at a temp path.
2. Call `StoreLogs` with a known entry. Confirm `nil` return.
3. Create a new `FileLogStore` on the same path (simulating reboot).
4. Call `GetLog` for the written index. Assert the returned entry equals the written entry exactly (same `Index`, `Term`, `Data`).

### US-2 — Rename atomicity is explicitly enforced

As a node operator, the rename of `raft_wal.json.tmp` to `raft_wal.json` must be explicitly durable. After a crash the filesystem must never be in a state where the old file is gone and the new file has not yet appeared in the directory.

`writeFileAtomically` is the single function that performs all atomic writes in the `durable_raft` package. It is called by:
- `persistLocked` (WAL)
- `SetState` (stable store)
- `SaveSnapshot` (snapshot store)

The fix to `writeFileAtomically` in Section 5.1 is the complete and only change required for all three callers. No change to `SaveSnapshot` source code is needed beyond what is provided by `writeFileAtomically` returning a real error instead of `nil`.

**Acceptance test** (exact — see Section 7.2, `TestWriteFileAtomically_DirSyncErrorPropagates`):
1. The test writes to a path inside `t.TempDir()`.
2. After a successful write, confirm the file exists and is readable.
3. The test confirms `writeFileAtomically` returns `nil` on a normal write (i.e. the success path still works after the change).

### US-3 — Corrupt WAL is rejected with a typed error

As a node starting after an unclean shutdown, the WAL loader must reject a file whose CRC does not match with `ErrWALCorrupt`, so that garbage is never applied to the state machine.

**Acceptance test** (exact — see Section 7.2, `TestFileLogStore_CorruptCRCReturnsErrWALCorrupt`):
1. Create a `FileLogStore` and write one entry.
2. Read the raw file bytes. Flip the byte at index `len(bytes)/2` by XOR with `0xFF`.
3. Write the corrupted bytes back.
4. Call `NewFileLogStore` on the same path.
5. Assert the returned error satisfies `errors.Is(err, ErrWALCorrupt)`.

### US-4 — Missing WAL is treated as empty (backward compatible)

As a node starting for the first time, `NewFileLogStore` must succeed and return an empty log when no WAL file exists, so that first-boot behaviour is unchanged.

**Acceptance test** (exact — see Section 7.2, `TestFileLogStore_MissingFileIsEmpty`):
1. Call `NewFileLogStore` with a path that does not exist.
2. Assert `err == nil`.
3. Call `LastIndexAndTerm`. Assert `index == 0`, `term == 0`, `err == nil`.

### US-5 — StableStore writes are CRC-protected

As a Raft node, when `currentTerm` and `votedFor` are persisted, a torn write must not cause the node to vote for a phantom candidate in a future term.

**Acceptance test** (exact — see Section 7.2, `TestFileStableStore_CorruptCRCReturnsErrStableCorrupt`):
1. Create a `FileStableStore` at a temp path.
2. Call `SetState(5, "node-1")`. Confirm `nil` return.
3. Read the raw file bytes. Flip the byte at index `len(bytes)/2` by XOR with `0xFF`.
4. Write the corrupted bytes back.
5. Call `GetState`.
6. Assert the returned error satisfies `errors.Is(err, ErrStableCorrupt)`.

---

## 3. Data Models

All struct and interface changes are confined to `internal/metadata_replicator/durable_raft/stores.go` and `internal/metadata_replicator/durable_raft/models.go`. The one call-site change in `servers/node/wire_grpc_etcd.go` is not a data model change and is covered in Section 5.6.

### 3.1 New error sentinels (stores.go)

Replace the existing `var` block at the top of `stores.go` with the following. The only additions are `ErrWALCorrupt` and `ErrStableCorrupt`:

```go
var (
    ErrLogNotFound   = errors.New("log entry not found")
    ErrLogCompacted  = errors.New("log entry compacted")
    ErrWALCorrupt    = errors.New("wal file failed checksum validation")
    ErrStableCorrupt = errors.New("stable store file failed checksum validation")
)
```

### 3.2 walEnvelope (stores.go)

Add this struct immediately after the `walFile` struct definition in `stores.go`. Do not modify `walFile`.

```go
// walEnvelope is the on-disk container for the WAL file.
// Payload is the raw JSON encoding of walFile.
// CRC is crc32.ChecksumIEEE(Payload).
// A mismatch between the stored CRC and a freshly computed checksum returns ErrWALCorrupt.
type walEnvelope struct {
    CRC     uint32 `json:"crc"`
    Payload []byte `json:"payload"`
}
```

The existing `walFile` struct is reproduced here for reference and must not be changed:

```go
type walFile struct {
    CompactedUntil uint64                     `json:"compacted_until"`
    CompactedTerm  uint64                     `json:"compacted_term"`
    Logs           []raft_replicator.LogEntry `json:"logs"`
}
```

### 3.3 stableEnvelope (stores.go)

Add this struct immediately after the `stableState` struct definition in `stores.go`. Do not modify `stableState`.

```go
// stableEnvelope is the on-disk container for the stable state file.
// Payload is the raw JSON encoding of stableState.
// CRC is crc32.ChecksumIEEE(Payload).
// A mismatch between the stored CRC and a freshly computed checksum returns ErrStableCorrupt.
type stableEnvelope struct {
    CRC     uint32 `json:"crc"`
    Payload []byte `json:"payload"`
}
```

The existing `stableState` struct is reproduced here for reference and must not be changed:

```go
type stableState struct {
    Term     uint64 `json:"term"`
    VotedFor string `json:"voted_for"`
}
```

### 3.4 Updated LogStore contract comment (models.go)

Replace the existing `LogStore` interface block in `models.go` with the following in its entirety:

```go
// LogStore (WAL): Append-only log persistence.
type LogStore interface {
    // StoreLogs persists a batch of entries to stable storage.
    // Implementations MUST complete all of the following before returning nil:
    //   1. Marshal entries and wrap in a walEnvelope with a CRC32 checksum.
    //   2. Write the envelope bytes to a temporary file (.tmp suffix).
    //   3. Call Sync() on the temporary file descriptor.
    //   4. Close the temporary file descriptor.
    //   5. Call os.Rename to atomically replace the final path.
    //   6. Open the parent directory and call Sync() on it.
    // A nil return is a durable append guarantee.
    // Any error in steps 1-6 must be returned to the caller.
    StoreLogs(entries []raft_replicator.LogEntry) error

    // GetLog retrieves the entry at the given Raft index.
    // Returns ErrLogCompacted if the index has been compacted.
    // Returns ErrLogNotFound if the index was never written.
    GetLog(index uint64) (raft_replicator.LogEntry, error)

    // LastIndexAndTerm returns the index and term of the last log entry.
    // Returns (0, 0, nil) if the log is empty.
    LastIndexAndTerm() (index uint64, term uint64, err error)

    // DeleteRange compacts the log by deleting entries in [min, max] inclusive.
    DeleteRange(min uint64, max uint64) error
}
```

---

## 4. New Wiring

### What changes

`NewFileStableStore` currently returns `*FileStableStore`. After this SDDD it returns `(*FileStableStore, error)`. There is exactly one call site for this constructor: `servers/node/wire_grpc_etcd.go`.

### Exact before/after diff for wire_grpc_etcd.go

**Before** (current line in `wire_grpc_etcd.go`):
```go
stableStore := durableraft.NewFileStableStore(opts.DataDir + "/raft_stable.json")
```

**After** (exact replacement — these two lines replace the one line above):
```go
stableStore, err := durableraft.NewFileStableStore(opts.DataDir + "/raft_stable.json")
if err != nil {
    panic(err)
}
```

This is the complete and only change to `wire_grpc_etcd.go`. No other line in that file is modified.

### Migration note

Files written by the old code (raw JSON without an envelope) will cause `NewFileLogStore` and `NewFileStableStore` to return `ErrWALCorrupt` / `ErrStableCorrupt` respectively when the new code reads them. These errors propagate to the existing `if err != nil { panic(err) }` guards in `Build()`. The accepted migration path for this research prototype is to wipe the data directory on upgrade. This is documented in a comment inside `load()` — see Section 5.3.

---

## 5. Exact Go Implementation

Replace each named function in `stores.go` with the exact body shown below. Every other function in `stores.go` that is not listed here must remain unchanged.

### 5.1 writeFileAtomically (stores.go)

```go
func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return err
    }

    tmpPath := path + ".tmp"
    f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
    if err != nil {
        return err
    }

    _, writeErr := f.Write(data)
    if writeErr == nil {
        writeErr = f.Sync()
    }
    closeErr := f.Close()
    if writeErr != nil {
        _ = os.Remove(tmpPath)
        return writeErr
    }
    if closeErr != nil {
        _ = os.Remove(tmpPath)
        return closeErr
    }

    if err := os.Rename(tmpPath, path); err != nil {
        _ = os.Remove(tmpPath)
        return err
    }

    // Directory fsync is mandatory. A rename is only crash-safe once the
    // parent directory entry is flushed to stable storage.
    d, err := os.Open(dir)
    if err != nil {
        return fmt.Errorf("open dir for fsync after rename: %w", err)
    }
    defer d.Close()
    if err := d.Sync(); err != nil {
        return fmt.Errorf("fsync dir after rename: %w", err)
    }
    return nil
}
```

### 5.2 persistLocked (stores.go)

```go
func (s *FileLogStore) persistLocked() error {
    entries := make([]raft_replicator.LogEntry, 0, len(s.logs))
    for _, entry := range s.logs {
        entries = append(entries, entry)
    }
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].Index < entries[j].Index
    })

    inner, err := json.Marshal(walFile{
        CompactedUntil: s.compactedUntil,
        CompactedTerm:  s.compactedTerm,
        Logs:           entries,
    })
    if err != nil {
        return fmt.Errorf("marshal wal inner: %w", err)
    }

    outer, err := json.Marshal(walEnvelope{
        CRC:     crc32.ChecksumIEEE(inner),
        Payload: inner,
    })
    if err != nil {
        return fmt.Errorf("marshal wal envelope: %w", err)
    }

    return writeFileAtomically(s.path, outer, 0o600)
}
```

### 5.3 load (stores.go)

```go
func (s *FileLogStore) load() error {
    raw, err := os.ReadFile(s.path)
    if os.IsNotExist(err) {
        // First boot: no WAL file yet. Correct and expected.
        return nil
    }
    if err != nil {
        return fmt.Errorf("read wal file: %w", err)
    }

    // NOTE: WAL files written by code prior to this envelope format do not
    // contain a "crc" or "payload" field. json.Unmarshal will succeed but
    // leave both fields at their zero values, causing the CRC check below to
    // fail. This returns ErrWALCorrupt. The accepted migration path for this
    // research prototype is to wipe the data directory on upgrade.
    var envelope walEnvelope
    if err := json.Unmarshal(raw, &envelope); err != nil {
        return fmt.Errorf("%w: envelope unmarshal failed: %v", ErrWALCorrupt, err)
    }
    if crc32.ChecksumIEEE(envelope.Payload) != envelope.CRC {
        return fmt.Errorf("%w: crc mismatch in %s", ErrWALCorrupt, s.path)
    }

    var wal walFile
    if err := json.Unmarshal(envelope.Payload, &wal); err != nil {
        return fmt.Errorf("%w: inner wal unmarshal failed: %v", ErrWALCorrupt, err)
    }

    s.compactedUntil = wal.CompactedUntil
    s.compactedTerm = wal.CompactedTerm
    s.last = wal.CompactedUntil

    for _, entry := range wal.Logs {
        idx := uint64(entry.Index)
        s.logs[idx] = entry
        if idx > s.last {
            s.last = idx
        }
    }

    return nil
}
```

### 5.4 SetState (stores.go)

```go
func (s *FileStableStore) SetState(currentTerm uint64, votedFor string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    inner, err := json.Marshal(stableState{Term: currentTerm, VotedFor: votedFor})
    if err != nil {
        return fmt.Errorf("marshal stable state: %w", err)
    }

    outer, err := json.Marshal(stableEnvelope{
        CRC:     crc32.ChecksumIEEE(inner),
        Payload: inner,
    })
    if err != nil {
        return fmt.Errorf("marshal stable envelope: %w", err)
    }

    return writeFileAtomically(s.path, outer, 0o600)
}
```

### 5.5 GetState (stores.go)

```go
func (s *FileStableStore) GetState() (uint64, string, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    raw, err := os.ReadFile(s.path)
    if os.IsNotExist(err) {
        return 0, "", nil
    }
    if err != nil {
        return 0, "", fmt.Errorf("read stable file: %w", err)
    }

    var envelope stableEnvelope
    if err := json.Unmarshal(raw, &envelope); err != nil {
        return 0, "", fmt.Errorf("%w: envelope unmarshal failed: %v", ErrStableCorrupt, err)
    }
    if crc32.ChecksumIEEE(envelope.Payload) != envelope.CRC {
        return 0, "", fmt.Errorf("%w: crc mismatch in %s", ErrStableCorrupt, s.path)
    }

    var state stableState
    if err := json.Unmarshal(envelope.Payload, &state); err != nil {
        return 0, "", fmt.Errorf("%w: inner stable unmarshal failed: %v", ErrStableCorrupt, err)
    }

    return state.Term, state.VotedFor, nil
}
```

### 5.6 NewFileStableStore (stores.go and wire_grpc_etcd.go)

**stores.go** — replace the existing `NewFileStableStore` function with:

```go
func NewFileStableStore(path string) (*FileStableStore, error) {
    _ = os.MkdirAll(filepath.Dir(path), 0o755)
    s := &FileStableStore{path: path}
    // Eagerly validate: if a file exists at path, confirm it is CRC-valid
    // before returning. A corrupt file here panics the node at startup via
    // the caller in wire_grpc_etcd.go, which is the correct behaviour.
    if _, _, err := s.GetState(); err != nil {
        return nil, fmt.Errorf("stable store validation on open: %w", err)
    }
    return s, nil
}
```

**wire_grpc_etcd.go** — replace the single existing constructor call:

Before:
```go
stableStore := durableraft.NewFileStableStore(opts.DataDir + "/raft_stable.json")
```

After:
```go
stableStore, err := durableraft.NewFileStableStore(opts.DataDir + "/raft_stable.json")
if err != nil {
    panic(err)
}
```

### 5.7 Required import addition (stores.go)

Replace the existing import block in `stores.go` with the following. The only addition is `"hash/crc32"`:

```go
import (
    "encoding/json"
    "errors"
    "fmt"
    "hash/crc32"
    "math"
    "os"
    "path/filepath"
    "sort"
    "sync"

    "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
)
```

---

## 6. Codebase Impact

### Files that MUST be changed

| File | What changes |
|------|-------------|
| `internal/metadata_replicator/durable_raft/stores.go` | Add `walEnvelope`, `stableEnvelope`, `ErrWALCorrupt`, `ErrStableCorrupt`; replace `writeFileAtomically`, `persistLocked`, `load`, `SetState`, `GetState`, `NewFileStableStore`; update import block |
| `internal/metadata_replicator/durable_raft/models.go` | Replace `LogStore` interface block with updated contract comment |
| `servers/node/wire_grpc_etcd.go` | Replace single `NewFileStableStore` call with two-line error-handled version |

### Files that MUST NOT be changed

- `internal/metadata_replicator/durable_raft/replicator.go`
- `internal/metadata_replicator/raft_replicator/` (all files)
- `internal/orchestrators/raft_tx_coordinator.go`
- `internal/chunk_service/` (all files)
- `internal/metadata_service/` (all files)
- `clients/durability_smoke/main.go`
- `clients/open_smoke/main.go`
- All protobuf generated files
- Every other file not in the MUST-change table above

---

## 7. Testing

### 7.1 Existing smoke suites — exact commands and pass criteria

Both suites must be run against a freshly started 3-node cluster with an empty data directory. Run them in the order listed below.

**Open smoke suite:**
```
go run ./clients/open_smoke/main.go
```
Pass condition: process exits with code 0.  
Fail condition: process exits with a non-zero code, or any line of output begins with `log.Fatal`.

**Durability smoke suite:**
```
go run ./clients/durability_smoke/main.go
```
Pass condition: process exits with code 0 and the final line of stdout is exactly:
```
All durability smoke tests passed
```
Fail condition: process exits with a non-zero code, or the final line of stdout is not the string above.

No other cluster configuration, node count, or environment is valid for this regression gate. If either command does not satisfy its pass condition after the SDDD changes are applied, the implementation is broken and must not be merged.

### 7.2 New unit tests — exact Go source

Create the file `internal/metadata_replicator/durable_raft/stores_test.go` with exactly the following content. Do not add, remove, or rename any test function:

```go
package durable_raft

import (
    "errors"
    "os"
    "testing"

    "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
)

// TestFileLogStore_MissingFileIsEmpty confirms US-4: first boot with no WAL
// file returns an empty log and no error.
func TestFileLogStore_MissingFileIsEmpty(t *testing.T) {
    path := t.TempDir() + "/raft_wal.json"

    s, err := NewFileLogStore(path)
    if err != nil {
        t.Fatalf("expected nil error on missing file, got: %v", err)
    }

    idx, term, err := s.LastIndexAndTerm()
    if err != nil {
        t.Fatalf("LastIndexAndTerm error: %v", err)
    }
    if idx != 0 || term != 0 {
        t.Fatalf("expected (0,0), got (%d,%d)", idx, term)
    }
}

// TestStoreLogs_SurvivesReload confirms US-1: an entry written via StoreLogs
// is readable after constructing a new FileLogStore on the same path.
func TestStoreLogs_SurvivesReload(t *testing.T) {
    path := t.TempDir() + "/raft_wal.json"

    s, err := NewFileLogStore(path)
    if err != nil {
        t.Fatalf("NewFileLogStore: %v", err)
    }

    entry := raft_replicator.LogEntry{Index: 1, Term: 2, Data: []byte("hello")}
    if err := s.StoreLogs([]raft_replicator.LogEntry{entry}); err != nil {
        t.Fatalf("StoreLogs: %v", err)
    }

    s2, err := NewFileLogStore(path)
    if err != nil {
        t.Fatalf("NewFileLogStore on reload: %v", err)
    }

    got, err := s2.GetLog(1)
    if err != nil {
        t.Fatalf("GetLog: %v", err)
    }
    if got.Index != entry.Index || got.Term != entry.Term || string(got.Data) != string(entry.Data) {
        t.Fatalf("entry mismatch: got %+v, want %+v", got, entry)
    }
}

// TestFileLogStore_CorruptCRCReturnsErrWALCorrupt confirms US-3: a file whose
// CRC does not match its payload causes NewFileLogStore to return ErrWALCorrupt.
func TestFileLogStore_CorruptCRCReturnsErrWALCorrupt(t *testing.T) {
    path := t.TempDir() + "/raft_wal.json"

    s, err := NewFileLogStore(path)
    if err != nil {
        t.Fatalf("NewFileLogStore: %v", err)
    }
    if err := s.StoreLogs([]raft_replicator.LogEntry{{Index: 1, Term: 1, Data: []byte("data")}}); err != nil {
        t.Fatalf("StoreLogs: %v", err)
    }

    raw, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("ReadFile: %v", err)
    }
    mid := len(raw) / 2
    raw[mid] ^= 0xFF
    if err := os.WriteFile(path, raw, 0o600); err != nil {
        t.Fatalf("WriteFile: %v", err)
    }

    _, err = NewFileLogStore(path)
    if !errors.Is(err, ErrWALCorrupt) {
        t.Fatalf("expected ErrWALCorrupt, got: %v", err)
    }
}

// TestFileLogStore_TruncatedFileReturnsErrWALCorrupt confirms that a file
// truncated to half its size also returns ErrWALCorrupt on load.
func TestFileLogStore_TruncatedFileReturnsErrWALCorrupt(t *testing.T) {
    path := t.TempDir() + "/raft_wal.json"

    s, err := NewFileLogStore(path)
    if err != nil {
        t.Fatalf("NewFileLogStore: %v", err)
    }
    if err := s.StoreLogs([]raft_replicator.LogEntry{{Index: 1, Term: 1, Data: []byte("data")}}); err != nil {
        t.Fatalf("StoreLogs: %v", err)
    }

    raw, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("ReadFile: %v", err)
    }
    if err := os.WriteFile(path, raw[:len(raw)/2], 0o600); err != nil {
        t.Fatalf("WriteFile: %v", err)
    }

    _, err = NewFileLogStore(path)
    if !errors.Is(err, ErrWALCorrupt) {
        t.Fatalf("expected ErrWALCorrupt, got: %v", err)
    }
}

// TestFileStableStore_CorruptCRCReturnsErrStableCorrupt confirms US-5: a
// stable store file whose CRC does not match causes GetState to return
// ErrStableCorrupt.
func TestFileStableStore_CorruptCRCReturnsErrStableCorrupt(t *testing.T) {
    path := t.TempDir() + "/raft_stable.json"

    s, err := NewFileStableStore(path)
    if err != nil {
        t.Fatalf("NewFileStableStore: %v", err)
    }
    if err := s.SetState(5, "node-1"); err != nil {
        t.Fatalf("SetState: %v", err)
    }

    raw, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("ReadFile: %v", err)
    }
    mid := len(raw) / 2
    raw[mid] ^= 0xFF
    if err := os.WriteFile(path, raw, 0o600); err != nil {
        t.Fatalf("WriteFile: %v", err)
    }

    _, _, err = s.GetState()
    if !errors.Is(err, ErrStableCorrupt) {
        t.Fatalf("expected ErrStableCorrupt, got: %v", err)
    }
}

// TestFileLogStore_RoundTrip confirms that multiple entries survive a full
// StoreLogs / reload / GetLog / LastIndexAndTerm round trip with exact values.
func TestFileLogStore_RoundTrip(t *testing.T) {
    path := t.TempDir() + "/raft_wal.json"

    s, err := NewFileLogStore(path)
    if err != nil {
        t.Fatalf("NewFileLogStore: %v", err)
    }

    entries := []raft_replicator.LogEntry{
        {Index: 1, Term: 1, Data: []byte("alpha")},
        {Index: 2, Term: 1, Data: []byte("beta")},
        {Index: 3, Term: 2, Data: []byte("gamma")},
    }
    if err := s.StoreLogs(entries); err != nil {
        t.Fatalf("StoreLogs: %v", err)
    }

    s2, err := NewFileLogStore(path)
    if err != nil {
        t.Fatalf("NewFileLogStore on reload: %v", err)
    }

    for _, want := range entries {
        got, err := s2.GetLog(uint64(want.Index))
        if err != nil {
            t.Fatalf("GetLog(%d): %v", want.Index, err)
        }
        if got.Index != want.Index || got.Term != want.Term || string(got.Data) != string(want.Data) {
            t.Fatalf("entry %d mismatch: got %+v, want %+v", want.Index, got, want)
        }
    }

    idx, term, err := s2.LastIndexAndTerm()
    if err != nil {
        t.Fatalf("LastIndexAndTerm: %v", err)
    }
    if idx != 3 || term != 2 {
        t.Fatalf("expected LastIndexAndTerm (3,2), got (%d,%d)", idx, term)
    }
}
```

Run the unit tests with:
```
go test ./internal/metadata_replicator/durable_raft/...
```

Pass condition: all six test functions pass and the command exits 0.  
Fail condition: any test function fails or the command exits non-zero.

### 7.3 What these tests do NOT cover (deferred to Blocker #4 SDDD)

- Acknowledged write survival across a hard process kill.
- Interrupted rename correctness under crash.
- Multi-node crash scenarios.

These are explicitly out of scope for this SDDD and are addressed by the Blocker #4 crash-consistency harness upgrade.

---

## 8. Resolved Design Decisions

**WAL format: whole-file JSON rewrite vs record-oriented append**
Decision: whole-file JSON rewrite. The vision paper does not claim log-structured append performance. Record-oriented format is a future improvement.

**Checksum algorithm: CRC32 vs SHA-256**
Decision: `crc32.ChecksumIEEE`. Detects torn writes and bit flips with minimal overhead. Tamper resistance is not a goal of this system.

**Corrupt file at startup: panic vs return error**
Decision: panic, via the existing `if err != nil { panic(err) }` guards already present in `wire_grpc_etcd.go`. A node that starts with a corrupt WAL would apply garbage to the state machine, which is worse than crashing. The operator must wipe the data directory and rejoin from a snapshot.

**Migration path for existing data directories**
Decision: wipe the data directory on upgrade. Pre-envelope files cause `ErrWALCorrupt` on load, which panics the node. This is documented in the `load()` comment in Section 5.3. No in-place migration logic is implemented.
