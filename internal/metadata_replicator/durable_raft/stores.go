package durable_raft

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

var (
	ErrLogNotFound   = errors.New("log entry not found")
	ErrLogCompacted  = errors.New("log entry compacted")
	ErrWALCorrupt    = errors.New("wal file failed checksum validation")
	ErrStableCorrupt = errors.New("stable store file failed checksum validation")
)

type stableState struct {
	Term     uint64 `json:"term"`
	VotedFor string `json:"voted_for"`
}

// stableEnvelope is the on-disk container for the stable state file.
// Payload is the raw JSON encoding of stableState.
// CRC is crc32.ChecksumIEEE(Payload).
// A mismatch between the stored CRC and a freshly computed checksum returns ErrStableCorrupt.
type stableEnvelope struct {
	CRC     uint32 `json:"crc"`
	Payload []byte `json:"payload"`
}

type snapshotFile struct {
	Meta SnapshotMeta `json:"meta"`
	Data []byte       `json:"data"`
}

type walFile struct {
	CompactedUntil uint64                     `json:"compacted_until"`
	CompactedTerm  uint64                     `json:"compacted_term"`
	Logs           []raft_replicator.LogEntry `json:"logs"`
}

// walEnvelope is the on-disk container for the WAL file.
// Payload is the raw JSON encoding of walFile.
// CRC is crc32.ChecksumIEEE(Payload).
// A mismatch between the stored CRC and a freshly computed checksum returns ErrWALCorrupt.
type walEnvelope struct {
	CRC     uint32 `json:"crc"`
	Payload []byte `json:"payload"`
}

type FileStableStore struct {
	path string
	mu   sync.Mutex
}

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

type FileSnapshotStore struct {
	path string
	mu   sync.Mutex
}

func NewFileSnapshotStore(path string) *FileSnapshotStore {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return &FileSnapshotStore{path: path}
}

func (s *FileSnapshotStore) SaveSnapshot(meta SnapshotMeta, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := json.Marshal(snapshotFile{Meta: meta, Data: data})
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	return writeFileAtomically(s.path, payload, 0o600)
}

func (s *FileSnapshotStore) LoadSnapshot() (SnapshotMeta, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return SnapshotMeta{}, nil, nil
	}
	if err != nil {
		return SnapshotMeta{}, nil, err
	}

	var snap snapshotFile
	if err := json.Unmarshal(payload, &snap); err != nil {
		return SnapshotMeta{}, nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return snap.Meta, snap.Data, nil
}

type FileLogStore struct {
	path string

	mu             sync.Mutex
	logs           map[uint64]raft_replicator.LogEntry
	last           uint64
	compactedUntil uint64
	compactedTerm  uint64
}

func NewFileLogStore(path string) (*FileLogStore, error) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	s := &FileLogStore{
		path: path,
		logs: make(map[uint64]raft_replicator.LogEntry),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

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

func (s *FileLogStore) StoreLogs(entries []raft_replicator.LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range entries {
		idx := uint64(entry.Index)
		if idx <= s.compactedUntil {
			return fmt.Errorf("cannot append compacted index %d <= %d", idx, s.compactedUntil)
		}
		s.logs[idx] = entry
		if idx > s.last {
			s.last = idx
		}
	}

	return s.persistLocked()
}

func (s *FileLogStore) GetLog(index uint64) (raft_replicator.LogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if index <= s.compactedUntil {
		return raft_replicator.LogEntry{}, ErrLogCompacted
	}
	entry, ok := s.logs[index]
	if !ok {
		return raft_replicator.LogEntry{}, ErrLogNotFound
	}
	return entry, nil
}

func (s *FileLogStore) LastIndexAndTerm() (uint64, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.last == 0 {
		return 0, 0, nil
	}
	if s.last == s.compactedUntil {
		return s.compactedUntil, s.compactedTerm, nil
	}
	entry, ok := s.logs[s.last]
	if !ok {
		return 0, 0, fmt.Errorf("last log index %d missing", s.last)
	}
	return s.last, uint64(entry.Term), nil
}

func (s *FileLogStore) DeleteRange(min, max uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if min > max {
		return nil
	}

	if max == math.MaxUint64 || max > s.last {
		max = s.last
	}
	if max == 0 {
		return nil
	}

	if min == 0 && max > s.compactedUntil {
		if entry, ok := s.logs[max]; ok {
			s.compactedTerm = uint64(entry.Term)
		}
		s.compactedUntil = max
	}

	if min <= s.compactedUntil {
		min = s.compactedUntil + 1
	}

	for idx := range s.logs {
		if idx >= min && idx <= max {
			delete(s.logs, idx)
		}
	}

	s.last = s.compactedUntil
	for idx := range s.logs {
		if idx > s.last {
			s.last = idx
		}
	}

	return s.persistLocked()
}

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
