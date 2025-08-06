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
	
}
