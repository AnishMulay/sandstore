package raft

import (
	"encoding/json"
	"reflect"
	"sync"
	"time"

	clusterraft "github.com/AnishMulay/sandstore/internal/cluster_service/raft"
	"github.com/AnishMulay/sandstore/internal/log_service"
	metadata_replicator "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	mrinternal "github.com/AnishMulay/sandstore/internal/metadata_replicator/internal"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
)

type RaftMetadataReplicator struct {
	clusterService *clusterraft.RaftClusterService
	metadataLog    *mrinternal.MetadataLog
	ls             log_service.LogService
	ms             metadata_service.MetadataService // For applying to state machine

	pendingMu  sync.Mutex
	pendingOps map[int64]chan error
}

func NewRaftMetadataReplicator(clusterService *clusterraft.RaftClusterService, ls log_service.LogService, ms metadata_service.MetadataService) *RaftMetadataReplicator {
	mr := &RaftMetadataReplicator{
		clusterService: clusterService,
		metadataLog:    mrinternal.NewMetadataLog(),
		ls:             ls,
		ms:             ms,
		pendingOps:     make(map[int64]chan error),
	}

	// Register this replicator as the log processor
	clusterService.SetLogProcessor(mr)

	return mr
}

func (mr *RaftMetadataReplicator) Replicate(op metadata_replicator.MetadataReplicationOp) error {
	if !mr.clusterService.IsLeader() {
		mr.ls.Info(log_service.LogEvent{
			Message:  "Replication failed - not leader",
			Metadata: map[string]any{"operation": op.Type},
		})
		return mrinternal.ErrNotLeader
	}

	entry := mrinternal.MetadataLogEntry{
		Term:      mr.clusterService.GetCurrentTerm(),
		Type:      op.Type,
		Operation: mr.convertToMetadataOperation(op),
		Timestamp: time.Now(),
	}

	logIndex := mr.metadataLog.AppendEntry(entry)

	respChan := make(chan error, 1)
	mr.pendingMu.Lock()
	mr.pendingOps[logIndex] = respChan
	mr.pendingMu.Unlock()

	entriesData, _ := json.Marshal([]mrinternal.MetadataLogEntry{entry})
	mr.clusterService.ReplicateEntries(entriesData, logIndex, mr.metadataLog, mr.onReplicationComplete)

	select {
	case err := <-respChan:
		mr.pendingMu.Lock()
		delete(mr.pendingOps, logIndex)
		mr.pendingMu.Unlock()
		return err

	case <-time.After(5 * time.Second):
		mr.pendingMu.Lock()
		delete(mr.pendingOps, logIndex)
		mr.pendingMu.Unlock()

		mr.ls.Info(log_service.LogEvent{
			Message:  "Replication timeout",
			Metadata: map[string]any{"logIndex": logIndex},
		})
		return mrinternal.ErrReplicationTimeout
	}
}

func (mr *RaftMetadataReplicator) convertToMetadataOperation(op metadata_replicator.MetadataReplicationOp) mrinternal.MetadataOperation {
	switch op.Type {
	case metadata_replicator.CREATE:
		return mrinternal.MetadataOperation{
			CreateOp: &mrinternal.CreateMetadataOp{
				Metadata: op.Metadata,
			},
		}
	case metadata_replicator.DELETE:
		return mrinternal.MetadataOperation{
			DeleteOp: &mrinternal.DeleteMetadataOp{
				Path: op.Metadata.Path,
			},
		}
	default:
		return mrinternal.MetadataOperation{}
	}
}

func (mr *RaftMetadataReplicator) ProcessReceivedEntries(entriesData []byte, prevLogIndex, prevLogTerm, leaderCommit int64) bool {
	mr.ls.Info(log_service.LogEvent{
		Message: "Processing received log entries",
		Metadata: map[string]any{
			"prevLogIndex": prevLogIndex,
			"prevLogTerm":  prevLogTerm,
			"leaderCommit": leaderCommit,
			"entriesSize":  len(entriesData),
		},
	})

	// Log consistency check
	if prevLogIndex > 0 {
		if prevLogIndex > mr.metadataLog.GetLastLogIndex() {
			mr.ls.Warn(log_service.LogEvent{
				Message: "Log consistency check failed - missing entries",
				Metadata: map[string]any{
					"prevLogIndex": prevLogIndex,
					"lastLogIndex": mr.metadataLog.GetLastLogIndex(),
				},
			})
			return false
		}

		prevEntry := mr.metadataLog.GetEntryAtIndex(prevLogIndex)
		if prevEntry != nil {
			if getTermFromEntry(prevEntry) != prevLogTerm {
				mr.ls.Warn(log_service.LogEvent{
					Message: "Log consistency check failed - term mismatch",
					Metadata: map[string]any{
						"prevLogIndex": prevLogIndex,
						"expectedTerm": prevLogTerm,
						"actualTerm":   getTermFromEntry(prevEntry),
					},
				})
				// Truncate conflicting entries
				mr.metadataLog.TruncateAfter(prevLogIndex)
				return false
			}
		}
	}

	// Deserialize and append entries (skip if empty - heartbeat case)
	if len(entriesData) > 0 {
		var entries []mrinternal.MetadataLogEntry
		if err := json.Unmarshal(entriesData, &entries); err != nil {
			mr.ls.Error(log_service.LogEvent{
				Message:  "Failed to deserialize log entries",
				Metadata: map[string]any{"error": err.Error()},
			})
			return false
		}

		for _, entry := range entries {
			mr.metadataLog.AppendEntry(entry)
			mr.ls.Debug(log_service.LogEvent{
				Message: "Appended log entry",
				Metadata: map[string]any{
					"index": entry.Index,
					"term":  entry.Term,
					"type":  entry.Type,
				},
			})
		}
	}

	// Update commit index if leader's is higher
	if leaderCommit > mr.metadataLog.GetCommitIndex() {
		newCommitIndex := min(leaderCommit, mr.metadataLog.GetLastLogIndex())
		mr.metadataLog.SetCommitIndex(newCommitIndex)
		mr.ls.Info(log_service.LogEvent{
			Message: "Updated commit index",
			Metadata: map[string]any{
				"oldCommitIndex": mr.metadataLog.GetCommitIndex(),
				"newCommitIndex": newCommitIndex,
			},
		})

		// Apply committed entries to state machine
		mr.applyCommittedEntries()
	}

	return true
}

// Helper function for min
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// Import getTermFromEntry from cluster_service
func getTermFromEntry(entry interface{}) int64 {
	if entry == nil {
		return 0
	}
	v := reflect.ValueOf(entry)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		termField := v.FieldByName("Term")
		if termField.IsValid() && termField.CanInterface() {
			if term, ok := termField.Interface().(int64); ok {
				return term
			}
		}
	}
	return 0
}

func (mr *RaftMetadataReplicator) applyCommittedEntries() {
	commitIndex := mr.metadataLog.GetCommitIndex()
	lastApplied := mr.metadataLog.GetLastApplied()

	if lastApplied >= commitIndex {
		return // Nothing to apply
	}

	mr.ls.Info(log_service.LogEvent{
		Message: "Applying committed entries to state machine",
		Metadata: map[string]any{
			"lastApplied": lastApplied,
			"commitIndex": commitIndex,
		},
	})

	// Apply entries from lastApplied+1 to commitIndex
	for i := lastApplied + 1; i <= commitIndex; i++ {
		entry := mr.metadataLog.GetEntryAtIndex(i)
		if entry == nil {
			mr.ls.Error(log_service.LogEvent{
				Message:  "Missing log entry during application",
				Metadata: map[string]any{"index": i},
			})
			continue
		}

		logEntry := entry.(*mrinternal.MetadataLogEntry)
		err := mr.applyLogEntry(logEntry)
		if err != nil {
			mr.ls.Error(log_service.LogEvent{
				Message: "Failed to apply log entry",
				Metadata: map[string]any{
					"index": i,
					"error": err.Error(),
				},
			})
			continue
		}

		mr.ls.Debug(log_service.LogEvent{
			Message: "Applied log entry to state machine",
			Metadata: map[string]any{
				"index": i,
				"type":  logEntry.Type,
			},
		})
	}

	// Update lastApplied
	mr.metadataLog.SetLastApplied(commitIndex)
}

func (mr *RaftMetadataReplicator) applyLogEntry(entry *mrinternal.MetadataLogEntry) error {
	switch entry.Type {
	case metadata_replicator.CREATE:
		if entry.Operation.CreateOp != nil {
			// Idempotent: check if metadata already exists
			_, err := mr.ms.GetFileMetadata(entry.Operation.CreateOp.Metadata.Path)
			if err == nil {
				// Metadata already exists, operation is idempotent
				mr.ls.Debug(log_service.LogEvent{
					Message:  "Metadata already exists, skipping create operation",
					Metadata: map[string]any{"path": entry.Operation.CreateOp.Metadata.Path},
				})
				return nil
			}
			return mr.ms.CreateFileMetadataFromStruct(entry.Operation.CreateOp.Metadata)
		}
	case metadata_replicator.DELETE:
		if entry.Operation.DeleteOp != nil {
			// Idempotent: check if metadata exists before deleting
			_, err := mr.ms.GetFileMetadata(entry.Operation.DeleteOp.Path)
			if err != nil {
				// Metadata doesn't exist, operation is idempotent
				mr.ls.Debug(log_service.LogEvent{
					Message:  "Metadata doesn't exist, skipping delete operation",
					Metadata: map[string]any{"path": entry.Operation.DeleteOp.Path},
				})
				return nil
			}
			return mr.ms.DeleteFileMetadata(entry.Operation.DeleteOp.Path)
		}
	}
	return nil
}

func (mr *RaftMetadataReplicator) onReplicationComplete(logIndex int64, success bool) {
	if success {
		mr.metadataLog.SetCommitIndex(logIndex)
		// Apply committed entries to state machine (for leader)
		mr.applyCommittedEntries()
	}

	mr.pendingMu.Lock()
	defer mr.pendingMu.Unlock()

	if respChan, exists := mr.pendingOps[logIndex]; exists {
		if success {
			respChan <- nil
		} else {
			respChan <- mrinternal.ErrMetadataReplicationFailed
		}
	}
}
