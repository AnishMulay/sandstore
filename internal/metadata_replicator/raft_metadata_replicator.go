package metadata_replicator

import (
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type RaftMetadataReplicator struct {
	clusterService *cluster_service.RaftClusterService
	metadataLog    *MetadataLog
	ls             log_service.LogService

	pendingMu  sync.Mutex
	pendingOps map[int64]chan error
}

func NewRaftMetadataReplicator(clusterService *cluster_service.RaftClusterService, ls log_service.LogService) *RaftMetadataReplicator {
	mr := &RaftMetadataReplicator{
		clusterService: clusterService,
		metadataLog:    NewMetadataLog(),
		ls:             ls,
		pendingOps:     make(map[int64]chan error),
	}
	
	// Register this replicator as the log processor
	clusterService.SetLogProcessor(mr)
	
	return mr
}

func (mr *RaftMetadataReplicator) Replicate(op MetadataReplicationOp) error {
	if !mr.clusterService.IsLeader() {
		mr.ls.Info(log_service.LogEvent{
			Message:  "Replication failed - not leader",
			Metadata: map[string]any{"operation": op.Type},
		})
		return ErrNotLeader
	}

	entry := MetadataLogEntry{
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

	entriesData, _ := json.Marshal([]MetadataLogEntry{entry})
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
		return ErrReplicationTimeout
	}
}

func (mr *RaftMetadataReplicator) convertToMetadataOperation(op MetadataReplicationOp) MetadataOperation {
	switch op.Type {
	case CREATE:
		return MetadataOperation{
			CreateOp: &CreateMetadataOp{
				Metadata: op.Metadata,
			},
		}
	case DELETE:
		return MetadataOperation{
			DeleteOp: &DeleteMetadataOp{
				Path: op.Metadata.Path,
			},
		}
	default:
		return MetadataOperation{}
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

	// Deserialize and append entries
	var entries []MetadataLogEntry
	if err := json.Unmarshal(entriesData, &entries); err != nil {
		mr.ls.Error(log_service.LogEvent{
			Message: "Failed to deserialize log entries",
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

func (mr *RaftMetadataReplicator) onReplicationComplete(logIndex int64, success bool) {
	if success {
		mr.metadataLog.SetCommitIndex(logIndex)
	}

	mr.pendingMu.Lock()
	defer mr.pendingMu.Unlock()
	
	if respChan, exists := mr.pendingOps[logIndex]; exists {
		if success {
			respChan <- nil
		} else {
			respChan <- ErrMetadataReplicationFailed
		}
	}
}