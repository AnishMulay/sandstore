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

	pendingMu  sync.Mutex
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