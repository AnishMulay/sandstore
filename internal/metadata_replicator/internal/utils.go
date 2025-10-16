package internal

import (
	"sync"
	"time"

	metadata_replicator "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
)

// might make this an interface later
type MetadataLogEntry struct {
	Index     int64                                     `json:"index"`
	Term      int64                                     `json:"term"`
	Type      metadata_replicator.MetadataOperationType `json:"type"`
	Operation MetadataOperation                         `json:"operation"`
	Timestamp time.Time                                 `json:"timestamp"`
}

type MetadataOperation struct {
	CreateOp *CreateMetadataOp `json:"create_op,omitempty"`
	DeleteOp *DeleteMetadataOp `json:"delete_op,omitempty"`
}

type CreateMetadataOp struct {
	Metadata metadata_service.FileMetadata `json:"metadata"`
}

type DeleteMetadataOp struct {
	Path string `json:"path"`
}

type MetadataLog struct {
	mu          sync.RWMutex
	entries     []MetadataLogEntry
	commitIndex int64
	lastApplied int64
}

func NewMetadataLog() *MetadataLog {
	return &MetadataLog{
		entries:     make([]MetadataLogEntry, 0),
		commitIndex: 0,
		lastApplied: 0,
	}
}

func (ml *MetadataLog) AppendEntry(entry MetadataLogEntry) int64 {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	entry.Index = int64(len(ml.entries)) + 1
	ml.entries = append(ml.entries, entry)
	return entry.Index
}

func (ml *MetadataLog) GetEntries(startIndex int64) []MetadataLogEntry {
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	if startIndex <= 0 || startIndex > int64(len(ml.entries)) {
		return []MetadataLogEntry{}
	}

	return ml.entries[startIndex-1:]
}

func (ml *MetadataLog) GetLastLogIndex() int64 {
	ml.mu.RLock()
	defer ml.mu.RUnlock()
	return int64(len(ml.entries))
}

func (ml *MetadataLog) GetLastLogTerm() int64 {
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	if len(ml.entries) == 0 {
		return 0
	}
	return ml.entries[len(ml.entries)-1].Term
}

func (ml *MetadataLog) GetEntryAtIndex(index int64) interface{} {
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	if index <= 0 || index > int64(len(ml.entries)) {
		return nil
	}

	return &ml.entries[index-1]
}

func (ml *MetadataLog) SetCommitIndex(index int64) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.commitIndex = index
}

func (ml *MetadataLog) GetCommitIndex() int64 {
	ml.mu.RLock()
	defer ml.mu.RUnlock()
	return ml.commitIndex
}

func (ml *MetadataLog) GetUncommittedEntries() []MetadataLogEntry {
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	if ml.lastApplied >= ml.commitIndex {
		return []MetadataLogEntry{}
	}

	start := ml.lastApplied
	end := ml.commitIndex

	if start < 0 {
		start = 0
	}
	if end > int64(len(ml.entries)) {
		end = int64(len(ml.entries))
	}

	return ml.entries[start:end]
}

func (ml *MetadataLog) SetLastApplied(index int64) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.lastApplied = index
}

func (ml *MetadataLog) GetLastApplied() int64 {
	ml.mu.RLock()
	defer ml.mu.RUnlock()
	return ml.lastApplied
}

func (ml *MetadataLog) TruncateAfter(index int64) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if index < 0 {
		ml.entries = []MetadataLogEntry{}
	} else if index < int64(len(ml.entries)) {
		ml.entries = ml.entries[:index]
	}
}
