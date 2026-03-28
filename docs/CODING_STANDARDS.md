# Sandstore Go Coding Standards

> This document defines the quality bar for all Go code in this repository.
> It is the reference used during code audits and code review.
> Sources: Effective Go, Google Go Style Guide, Uber Go Guide, and lessons
> learned from AI-assisted development on this codebase.

---

## 1. The guiding principle

Write code for the reader, not the compiler. A distributed systems project
like Sandstore will be read far more times than it is written. Every decision
should make the next reader's job easier.

---

## 2. Naming

### 2.1 Packages
- Package names are lowercase, single words, no underscores: `raft`, `etcd`, `grpccomm`
- Package names describe what they provide, not what they contain: `chunk` not `chunkutils`
- Never use placeholder names: `simple`, `default`, `base`, `common`, `util`, `helpers`
  are all banned as package names

### 2.2 Types and functions
- Exported types use MixedCaps: `RaftReplicator`, `EtcdClusterService`
- Acronyms are consistently cased: `gRPC` is `GRPC` in exported names, `URL` stays `URL`
- Constructors are named `New<Type>`: `NewRaftReplicator`, `NewEtcdClusterService`
- One-method interfaces are named by the method plus `-er`: `Reader`, `Writer`, `Replicator`
- Interface names describe capability, not implementation: `MetadataReplicator` not `RaftInterface`
- Never use `Legacy`, `Old`, `V1`, `V2`, `New` as adjectives in type names — version these with
  packages or files instead

### 2.3 Variables
- Short names for short scopes: `i`, `n`, `err`, `ctx` are idiomatic at function scope
- Longer names for longer scopes: package-level vars should be descriptive
- Unexported package-level vars and consts are prefixed with `_` to signal package scope:
  `var _defaultTimeout = 5 * time.Second`
- Boolean vars and fields are named to read naturally in an `if`: `isLeader`, `hasQuorum`,
  `enabled` — never `leaderBool` or `leaderFlag`

---

## 3. Interfaces

### 3.1 Define interfaces at the point of use
Interfaces belong in the package that *uses* them, not the package that implements them.
The implementing package returns concrete types. This is the most commonly violated rule
in AI-generated Go code.

**Wrong:**
```go
// In package raft — implementing package defines its own interface
type Replicator interface { Replicate(entry LogEntry) error }
type RaftReplicator struct{}
func (r *RaftReplicator) Replicate(entry LogEntry) error { ... }
```

**Right:**
```go
// In package orchestrators — consuming package defines what it needs
type MetadataReplicator interface { Replicate(entry metadata.Operation) error }

// In package raft — concrete type, no interface
type RaftReplicator struct{}
func (r *RaftReplicator) Replicate(entry metadata.Operation) error { ... }
```

### 3.2 Keep interfaces small
Prefer many small interfaces over one large one. A 10-method interface is almost always
wrong. If you find yourself implementing a large interface "for mocking," the design is wrong.

### 3.3 Do not define interfaces speculatively
An interface that has exactly one implementation and no tests that use it is dead weight.
Every interface in this codebase should have at least two implementations or a test that
depends on the abstraction.

---

## 4. Error handling

### 4.1 Always handle errors explicitly
Never assign to `_` when a function returns an error unless you have a documented reason.

### 4.2 Wrap errors with context
```go
// Wrong: loses context
if err != nil { return err }

// Right: adds context
if err != nil { return fmt.Errorf("replicating entry %d: %w", entry.Index, err) }
```

### 4.3 Error strings are lowercase and unpunctuated
Error strings are parts of larger messages. They do not start with capitals and do not
end with periods: `"failed to connect to etcd"` not `"Failed to connect to etcd."`.

### 4.4 No panics in library code
Panics are for unrecoverable programmer errors (nil dereference, impossible state).
Server startup failures return errors. No library function in `internal/` should panic
under any foreseeable condition.

### 4.5 No sentinel errors in new code
Use `fmt.Errorf("...: %w", err)` and `errors.Is` / `errors.As` for error inspection.
Do not define package-level `var ErrXxx = errors.New(...)` unless callers need to
distinguish that specific error in a switch.

---

## 5. Concurrency

### 5.1 Document goroutine ownership
Every `go func()` launch must have a comment explaining who owns the goroutine and how
it terminates. Undocumented goroutines are the primary source of leaks in this codebase.

### 5.2 Always respect context cancellation
Any function that does I/O, waits on a channel, or sleeps must accept a `context.Context`
as its first parameter and return when the context is cancelled.

### 5.3 Prefer channels for ownership transfer, mutexes for shared state
Use a channel when one goroutine hands data to another. Use a `sync.Mutex` when multiple
goroutines read and write shared state. Never use a channel as a mutex.

### 5.4 Never close a channel from the receiver
Only the goroutine that sends to a channel should close it. Closing from the receiver
causes panics when the sender tries to write.

### 5.5 sync.WaitGroup is always incremented before the goroutine starts
```go
// Wrong — race condition
go func() { wg.Add(1); defer wg.Done(); ... }()

// Right
wg.Add(1)
go func() { defer wg.Done(); ... }()
```

---

## 6. Code structure and length

### 6.1 Functions do one thing
If a function's name requires "and", it does too much. Split it.
Target: under 40 lines per function. Flag anything over 80 lines for review.

### 6.2 Keep the happy path at the left margin
Handle errors and early returns first. The main logic of a function should not be
nested inside an else block.
```go
// Wrong
if err == nil {
    // 30 lines of main logic
} else {
    return err
}

// Right
if err != nil {
    return fmt.Errorf("doing thing: %w", err)
}
// 30 lines of main logic at the left margin
```

### 6.3 No commented-out code
Dead code is deleted, not commented out. Use git history to retrieve old code.
Any commented-out block of code is an audit flag.

### 6.4 No TODO comments without an associated GitHub issue
`// TODO: fix this later` is banned. Either fix it now or open an issue and write
`// TODO(#42): fix X when Y is implemented`.

---

## 7. Comments and documentation

### 7.1 All exported symbols have godoc comments
Every exported type, function, method, and constant must have a godoc comment.
The comment starts with the name of the symbol: `// RaftReplicator manages...`

### 7.2 Comments explain why, not what
The code shows what. Comments explain why a decision was made, what invariant is
being maintained, or what a non-obvious value represents.
```go
// Wrong: restates the code
// Increment i by 1
i++

// Right: explains the invariant
// i starts at 1 because index 0 is reserved for the bootstrap entry
i := 1
```

### 7.3 No AI-style filler comments
These are common in AI-generated code and add no value. Delete them:
- `// Initialize the struct`
- `// Create a new instance of X`
- `// Call the method`
- `// Return the result`
- `// Handle the error`
- `// Check if err is not nil`

---

## 8. AI slop patterns — what to look for

These patterns are common in LLM-generated Go code and signal that a section needs
human review and likely rewriting:

### 8.1 Unnecessary abstraction layers
An interface with one method, one implementation, and no tests using the interface
as an abstraction. Remove the interface, use the concrete type.

### 8.2 Wrapper structs that add no behavior
```go
type MyWrapper struct { inner *SomeType }
func (w *MyWrapper) DoThing() { w.inner.DoThing() }
```
If the wrapper adds no fields, no validation, no logging, and no transformation —
it is slop. Delete it and use `*SomeType` directly.

### 8.3 Defensive nil checks for values that are never nil
```go
if client != nil {
    client.Close()
}
```
If `client` is always non-nil at this point, the check is noise. Document the
invariant instead or restructure so nil is impossible.

### 8.4 Over-parameterized functions
```go
func NewServer(host string, port int, timeout time.Duration, retries int,
    logLevel string, enableMetrics bool, metricsPort int, ...) *Server
```
More than 4-5 parameters almost always means the function needs a config struct.

### 8.5 Copied error handling boilerplate
Multiple consecutive blocks of identical `if err != nil { return err }` with no
additional context added is a sign that error wrapping was not thought through.

### 8.6 Spurious type aliases
```go
type NodeID = string
type Address = string
```
If the alias is never used to enforce type safety at compile time, it adds confusion.

### 8.7 Unused exported symbols
Any exported function, type, or variable that has zero callers outside its own package
is almost certainly AI slop from a previous iteration. Find it with `deadcode` or manual search.

### 8.8 Inconsistent receiver names
```go
func (r *RaftReplicator) Start() {}
func (repl *RaftReplicator) Stop() {}
func (rr *RaftReplicator) Status() {}
```
Receiver name must be consistent across all methods on a type. Pick one short name
and use it everywhere.

### 8.9 Magic numbers and strings without named constants
```go
time.Sleep(5 * time.Second)  // Why 5?
if len(nodes) < 3 { ... }    // Why 3?
```
Every magic value needs either a named constant with a comment explaining it, or
an inline comment explaining the specific constraint.

### 8.10 Test files that only test the happy path
A test suite with no error injection, no context cancellation, and no concurrent
access scenarios is incomplete. Each tested function should have at least one failure
case test.

---

## 9. Testing standards

### 9.1 Test function names describe the scenario
```go
// Wrong
func TestWrite(t *testing.T) {}

// Right
func TestWrite_ReturnsErrOnClosedFD(t *testing.T) {}
func TestWrite_FlushesBufferWhenFull(t *testing.T) {}
```

### 9.2 Use table-driven tests for multiple cases
Any test function with more than 2 scenarios should use a table-driven format with
named test cases.

### 9.3 Test helpers use t.Helper()
```go
func assertNoError(t *testing.T, err error) {
    t.Helper()  // Required — makes failure lines point to caller, not helper
    if err != nil { t.Fatalf("unexpected error: %v", err) }
}
```

### 9.4 No time.Sleep in tests
Tests that sleep are flaky tests waiting to happen. Use channels, sync primitives,
or polling with a timeout and `require.Eventually` pattern instead.

---

## 10. Distributed systems specific

### 10.1 All network calls have timeouts
Every gRPC call, etcd operation, and TCP dial must use a context with a timeout.
A network call without a timeout will hang forever on a network partition.

### 10.2 Raft state machine transitions are logged
Every state transition (follower → candidate → leader) must be logged at INFO level
with the term number and the reason for the transition.

### 10.3 Persistent state is written before responding
In a Raft implementation, log entries must be durably written to disk *before* the
AppendEntries response is sent. Writing after responding violates the protocol.

### 10.4 Client retries are bounded
No retry loop in this codebase should be unbounded. Every retry loop must have:
- A maximum number of attempts or a deadline
- Exponential backoff with jitter
- A log message on each failed attempt at DEBUG or WARN level

### 10.5 Metrics are emitted for every significant operation
Every significant codepath (write, read, replication, leader election, error) must
emit a Prometheus metric. Observability is not optional in a distributed system.

---

## 11. Tooling requirements

Every Go file in this repo must pass:
gofmt -l .           # zero output
go vet ./...         # zero issues
golangci-lint run    # zero issues (see .golangci.yml for enabled linters)
go build ./...       # clean build
go test ./...        # all tests pass

These are gates, not suggestions. A file that fails any of these is not mergeable.

---

*Last updated: 2026-03-27. Maintained by the Sandstore project.*
