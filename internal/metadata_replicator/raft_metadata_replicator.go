package metadata_replicator

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type RaftMetadataReplicator struct {
	clusterService *cluster_service.RaftClusterService
	metadataLog    *MetadataLog
	ls             log_service.LogService

	mu         sync.RWMutex
	pendingOps map[int64]chan error
}

func NewRaftMetadataReplicator(clusterService *cluster_service.RaftClusterService, ls log_service.LogService) *RaftMetadataReplicator {
	return &RaftMetadataReplicator{
		clusterService: clusterService,
		metadataLog:    NewMetadataLog(),
		ls:             ls,
		pendingOps:     make(map[int64]chan error),
	}
}

func (mr *RaftMetadataReplicator) Replicate(op MetadataReplicationOp) error {
	if !mr.clusterService.IsLeader() {
		return ErrNotLeader
	}

	mr.mu.Lock()
	defer mr.mu.Unlock()

	// Create log entry
	entry := MetadataLogEntry{
		Term:      mr.clusterService.GetCurrentTerm(),
		Type:      op.Type,
		Operation: mr.convertToMetadataOperation(op),
		Timestamp: time.Now(),
	}

	// Append to local log
	logIndex := mr.metadataLog.AppendEntry(entry)

	// Create response channel
	respChan := make(chan error, 1)
	mr.pendingOps[logIndex] = respChan

	// Delegate replication to cluster service
	entriesData, _ := json.Marshal([]MetadataLogEntry{entry})
	go mr.clusterService.ReplicateEntries(entriesData, logIndex, mr.metadataLog, mr.onReplicationComplete)

	// Wait for result
	select {
	case err := <-respChan:
		delete(mr.pendingOps, logIndex)
		return err
	case <-time.After(5 * time.Second):
		delete(mr.pendingOps, logIndex)
		return ErrReplicationTimeout
	}
}
// convertToMetadataOperation converts MetadataReplicationOp to MetadataOperation
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



// onReplicationComplete is called by cluster service when replication completes
func (mr *RaftMetadataReplicator) onReplicationComplete(logIndex int64, success bool) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	if success {
		mr.metadataLog.SetCommitIndex(logIndex)
	}

	// Notify waiting operation
	if respChan, exists := mr.pendingOps[logIndex]; exists {
		if success {
			respChan <- nil
		} else {
			respChan <- ErrMetadataReplicationFailed
		}
	}
}