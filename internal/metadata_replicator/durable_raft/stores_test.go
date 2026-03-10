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
