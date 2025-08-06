package metadata_replicator

import (
	"context"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

type RaftMetadataReplicator struct {
	clusterService cluster_service.ClusterService
	comm           communication.Communicator
	ls             log_service.LogService

	metadataLog *MetadataLog

	mu sync.RWMutex

	// pending ops for now, might change to something simpler later
	pendingOps  map[int64]chan error
	nextOpIndex int64

	ctx    context.Context
	cancel context.CancelFunc
}

func NewRaftMetadataReplicator(clusterService cluster_service.ClusterService, comm communication.Communicator, ls log_service.LogService) *RaftMetadataReplicator {
	ctx, cancel := context.WithCancel(context.Background())
	return &RaftMetadataReplicator{
		clusterService: clusterService,
		comm:           comm,
		ls:             ls,
		metadataLog:    NewMetadataLog(),
		pendingOps:     make(map[int64]chan error),
		nextOpIndex:    0,
		ctx:            ctx,
		cancel:         cancel,
	}
}

func (mr *RaftMetadataReplicator) Replicate(op MetadataReplicationOp) error {
	opType := "create"
	if op.Type == DELETE {
		opType = "delete"
	}

	mr.ls.Info(log_service.LogEvent{
		Message:  "Replicating metadata operation via Raft",
		Metadata: map[string]any{"path": op.Metadata.Path, "type": opType},
	})

	// Create log entry for the operation
	var logOp MetadataOperation
	if op.Type == CREATE {
		logOp = MetadataOperation{
			CreateOp: &CreateMetadataOp{Metadata: op.Metadata},
		}
	} else {
		logOp = MetadataOperation{
			DeleteOp: &DeleteMetadataOp{Path: op.Metadata.Path},
		}
	}

	entry := MetadataLogEntry{
		Type:      MetadataOpType(opType),
		Operation: logOp,
		Timestamp: time.Now(),
	}

	// Add to log and get index
	index := mr.metadataLog.AppendEntry(entry)

	// Create channel for this operation
	mr.mu.Lock()
	resultChan := make(chan error, 1)
	mr.pendingOps[index] = resultChan
	mr.mu.Unlock()

	// TODO: Implement actual Raft consensus here
	// For now, simulate successful replication
	go func() {
		// Simulate consensus delay
		time.Sleep(100 * time.Millisecond)
		resultChan <- nil
	}()

	// Wait for result
	select {
	case err := <-resultChan:
		mr.mu.Lock()
		delete(mr.pendingOps, index)
		mr.mu.Unlock()
		return err
	case <-mr.ctx.Done():
		mr.mu.Lock()
		delete(mr.pendingOps, index)
		mr.mu.Unlock()
		return mr.ctx.Err()
	}
}
