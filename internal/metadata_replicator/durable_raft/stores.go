package durable_raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
)

type FileStableStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStableStore(path string) *FileStableStore {
	os.MkdirAll(filepath.Dir(path), 0755)
	return &FileStableStore{path: path}
}

func (s *FileStableStore) SetState(currentTerm uint64, votedFor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"term": currentTerm, "votedFor": votedFor})
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_SYNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func (s *FileStableStore) GetState() (uint64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, "", err
	}
	term := uint64(state["term"].(float64))
	votedFor := state["votedFor"].(string)
	return term, votedFor, nil
}

type FileSnapshotStore struct {
	path string
	mu   sync.Mutex
}

func NewFileSnapshotStore(path string) *FileSnapshotStore {
	os.MkdirAll(filepath.Dir(path), 0755)
	return &FileSnapshotStore{path: path}
}

func (s *FileSnapshotStore) SaveSnapshot(meta SnapshotMeta, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	wrapper := map[string]any{
		"meta": meta,
		"data": data,
	}
	bytes, _ := json.Marshal(wrapper)

	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_SYNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(bytes); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, s.path)
}

func (s *FileSnapshotStore) LoadSnapshot() (SnapshotMeta, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bytes, err := os.ReadFile(s.path)
	if err != nil {
		return SnapshotMeta{}, nil, err
	}
	var wrapper struct {
		Meta SnapshotMeta `json:"meta"`
		Data []byte       `json:"data"`
	}
	if err := json.Unmarshal(bytes, &wrapper); err != nil {
		return SnapshotMeta{}, nil, err
	}
	return wrapper.Meta, wrapper.Data, nil
}

type MemoryLogStore struct {
	mu     sync.Mutex
	logs   map[uint64]raft_replicator.LogEntry
	last   uint64
	offset uint64 // For compaction
}

func NewMemoryLogStore() *MemoryLogStore {
	return &MemoryLogStore{
		logs: make(map[uint64]raft_replicator.LogEntry),
	}
}

func (s *MemoryLogStore) StoreLogs(entries []raft_replicator.LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		s.logs[uint64(e.Index)] = e
		if uint64(e.Index) > s.last {
			s.last = uint64(e.Index)
		}
	}
	// Simulate fsync
	time.Sleep(1 * time.Millisecond)
	return nil
}

func (s *MemoryLogStore) GetLog(index uint64) (raft_replicator.LogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.logs[index]; ok {
		return e, nil
	}
	return raft_replicator.LogEntry{}, fmt.Errorf("not found")
}

func (s *MemoryLogStore) LastIndexAndTerm() (uint64, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == 0 || s.last <= s.offset {
		return s.offset, 0, nil
	}
	return s.last, uint64(s.logs[s.last].Term), nil
}

func (s *MemoryLogStore) DeleteRange(min, max uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := min; i <= max; i++ {
		delete(s.logs, i)
	}
	if max > s.offset {
		s.offset = max
	}
	return nil
}
