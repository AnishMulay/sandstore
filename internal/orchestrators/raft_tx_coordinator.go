package orchestrators

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/domain"
	log_service "github.com/AnishMulay/sandstore/internal/log_service"
	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	rr "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/metrics"
)

const (
	// commitNotificationTimeout is the maximum time to wait for a commit or
	// abort notification to be delivered to a participant. This fan-out is
	// best-effort after the primary transaction outcome has already been
	// decided, so failures are logged but do not change the commit result.
	commitNotificationTimeout = 2 * time.Second
)

type RaftTransactionCoordinator struct {
	comm           communication.Communicator
	replicator     pmr.MetadataReplicator
	ls             log_service.LogService
	metricsService metrics.MetricsService
}

func NewRaftTransactionCoordinator(comm communication.Communicator, replicator pmr.MetadataReplicator, ls log_service.LogService, metricsService metrics.MetricsService) *RaftTransactionCoordinator {
	return &RaftTransactionCoordinator{
		comm:           comm,
		replicator:     replicator,
		ls:             ls,
		metricsService: metricsService,
	}
}

func (c *RaftTransactionCoordinator) NewTransaction(txnID string) *RaftTxHandle {
	return &RaftTxHandle{
		txnID:          txnID,
		comm:           c.comm,
		replicator:     c.replicator,
		ls:             c.ls,
		metricsService: c.metricsService,
	}
}

type RaftTxHandle struct {
	txnID          string
	comm           communication.Communicator
	replicator     pmr.MetadataReplicator
	ls             log_service.LogService
	metricsService metrics.MetricsService
}

func (h *RaftTxHandle) Init(ctx context.Context, chunkID string, participants []domain.ChunkLocation) error {
	start := time.Now()
	defer func() {
		if h == nil || h.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		h.metricsService.Observe(metrics.RaftTxCoordinatorInitLatency, elapsed, metrics.MetricTags{
			Operation: "init",
			Service:   "RaftTransactionCoordinator",
		})
	}()

	_ = ctx
	_ = chunkID
	_ = participants
	return nil
}

func (h *RaftTxHandle) Commit(ctx context.Context, chunkID string, metaUpdate *pms.MetadataOperation, participants []domain.ChunkLocation) error {
	start := time.Now()
	defer func() {
		if h == nil || h.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		h.metricsService.Observe(metrics.RaftTxCoordinatorCommitLatency, elapsed, metrics.MetricTags{
			Operation: "commit",
			Service:   "RaftTransactionCoordinator",
		})
	}()

	envelope := rr.RaftCommandEnvelope{
		Type: rr.PayloadChunkIntent,
		ChunkIntent: &rr.ChunkIntentOperation{
			TxnID:     h.txnID,
			ChunkID:   chunkID,
			NodeIDs:   extractParticipantIDs(participants),
			State:     rr.StateCommitted,
			Timestamp: time.Now().UnixNano(),
		},
		PosixMeta: metaUpdate,
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal raft envelope: %w", err)
	}
	if err := h.replicator.Replicate(ctx, payload); err != nil {
		return fmt.Errorf("consensus commit failed: %w", err)
	}

	h.broadcastCommitAsync(h.txnID, chunkID, participants)

	return nil
}

func (h *RaftTxHandle) Abort(ctx context.Context, chunkID string, participants []domain.ChunkLocation) error {
	start := time.Now()
	defer func() {
		if h == nil || h.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		h.metricsService.Observe(metrics.RaftTxCoordinatorAbortLatency, elapsed, metrics.MetricTags{
			Operation: "abort",
			Service:   "RaftTransactionCoordinator",
		})
	}()

	_ = ctx

	h.broadcastAbortAsync(h.txnID, chunkID, participants)
	return nil
}

func (h *RaftTxHandle) broadcastCommitAsync(txnID string, chunkID string, participants []domain.ChunkLocation) {
	start := time.Now()
	defer func() {
		if h == nil || h.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		h.metricsService.Observe(metrics.RaftTxCoordinatorBroadcastCommitAsyncLatency, elapsed, metrics.MetricTags{
			Operation: "broadcast_commit_async",
			Service:   "RaftTransactionCoordinator",
		})
	}()

	for _, participant := range participants {
		// This goroutine sends a best-effort commit notification after the
		// consensus commit has already succeeded. Its lifetime is bounded by
		// commitNotificationTimeout. Delivery failures are logged but not
		// retried or otherwise tracked in this coordinator.
		go func(participant domain.ChunkLocation) {
			ctx, cancel := context.WithTimeout(context.Background(), commitNotificationTimeout)
			defer cancel()

			msg := communication.Message{
				From: h.comm.Address(),
				Type: communication.MessageTypeCommitChunk,
				Payload: communication.CommitChunkRequest{
					TxnID:   txnID,
					ChunkID: chunkID,
				},
			}
			if _, err := h.comm.Send(ctx, participant.PhysicalEndpoint, msg); err != nil && h.ls != nil {
				h.ls.Warn(log_service.LogEvent{
					Message: "commit notification failed",
					Metadata: map[string]any{
						"participant": participant.PhysicalEndpoint,
						"txnID":       txnID,
						"chunkID":     chunkID,
						"error":       err.Error(),
					},
				})
			}
		}(participant)
	}
}

func (h *RaftTxHandle) broadcastAbortAsync(txnID string, chunkID string, participants []domain.ChunkLocation) {
	start := time.Now()
	defer func() {
		if h == nil || h.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		h.metricsService.Observe(metrics.RaftTxCoordinatorBroadcastAbortAsyncLatency, elapsed, metrics.MetricTags{
			Operation: "broadcast_abort_async",
			Service:   "RaftTransactionCoordinator",
		})
	}()

	for _, participant := range participants {
		// This goroutine sends a best-effort abort notification after the
		// transaction outcome has already been decided. Its lifetime is bounded
		// by commitNotificationTimeout. Delivery failures are logged but not
		// retried or otherwise tracked in this coordinator.
		go func(participant domain.ChunkLocation) {
			ctx, cancel := context.WithTimeout(context.Background(), commitNotificationTimeout)
			defer cancel()

			msg := communication.Message{
				From: h.comm.Address(),
				Type: communication.MessageTypeAbortChunk,
				Payload: communication.AbortChunkRequest{
					TxnID:   txnID,
					ChunkID: chunkID,
				},
			}
			if _, err := h.comm.Send(ctx, participant.PhysicalEndpoint, msg); err != nil && h.ls != nil {
				h.ls.Warn(log_service.LogEvent{
					Message: "abort notification failed",
					Metadata: map[string]any{
						"participant": participant.PhysicalEndpoint,
						"txnID":       txnID,
						"chunkID":     chunkID,
						"error":       err.Error(),
					},
				})
			}
		}(participant)
	}
}

func extractParticipantIDs(participants []domain.ChunkLocation) []string {
	nodeIDs := make([]string, 0, len(participants))
	for _, participant := range participants {
		nodeIDs = append(nodeIDs, participant.LogicalNodeAlias)
	}
	return nodeIDs
}
