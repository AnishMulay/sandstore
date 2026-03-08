package orchestrators

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/domain"
	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	rr "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
)

type RaftTransactionCoordinator struct {
	comm       communication.Communicator
	replicator pmr.MetadataReplicator
}

var _ TransactionCoordinator = (*RaftTransactionCoordinator)(nil)

func NewRaftTransactionCoordinator(comm communication.Communicator, replicator pmr.MetadataReplicator) *RaftTransactionCoordinator {
	return &RaftTransactionCoordinator{
		comm:       comm,
		replicator: replicator,
	}
}

func (c *RaftTransactionCoordinator) NewTransaction(txnID string) TxHandle {
	return &RaftTxHandle{
		txnID:      txnID,
		comm:       c.comm,
		replicator: c.replicator,
	}
}

type RaftTxHandle struct {
	txnID      string
	chunkID    string
	comm       communication.Communicator
	replicator pmr.MetadataReplicator
}

var _ TxHandle = (*RaftTxHandle)(nil)

func (h *RaftTxHandle) Init(ctx context.Context, chunkID string, participants []domain.ChunkLocation) error {
	_ = ctx
	_ = participants

	h.chunkID = chunkID
	return nil
}

func (h *RaftTxHandle) Commit(ctx context.Context, metaUpdate *pms.MetadataOperation, participants []domain.ChunkLocation) error {
	if h.chunkID == "" {
		return fmt.Errorf("transaction %s not initialized", h.txnID)
	}

	envelope := rr.RaftCommandEnvelope{
		Type: rr.PayloadChunkIntent,
		ChunkIntent: &rr.ChunkIntentOperation{
			TxnID:     h.txnID,
			ChunkID:   h.chunkID,
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

	h.broadcastCommitAsync(h.txnID, h.chunkID, participants)

	return nil
}

func (h *RaftTxHandle) Abort(ctx context.Context, participants []domain.ChunkLocation) error {
	_ = ctx

	if h.chunkID == "" {
		return fmt.Errorf("transaction %s not initialized", h.txnID)
	}

	h.broadcastAbortAsync(h.txnID, h.chunkID, participants)
	return nil
}

func (h *RaftTxHandle) broadcastCommitAsync(txnID string, chunkID string, participants []domain.ChunkLocation) {
	for _, participant := range participants {
		go func(participant domain.ChunkLocation) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			msg := communication.Message{
				From: h.comm.Address(),
				Type: communication.MessageTypeCommitChunk,
				Payload: communication.CommitChunkRequest{
					TxnID:   txnID,
					ChunkID: chunkID,
				},
			}
			_, _ = h.comm.Send(ctx, participant.PhysicalEndpoint, msg)
		}(participant)
	}
}

func (h *RaftTxHandle) broadcastAbortAsync(txnID string, chunkID string, participants []domain.ChunkLocation) {
	for _, participant := range participants {
		go func(participant domain.ChunkLocation) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			msg := communication.Message{
				From: h.comm.Address(),
				Type: communication.MessageTypeAbortChunk,
				Payload: communication.AbortChunkRequest{
					TxnID:   txnID,
					ChunkID: chunkID,
				},
			}
			_, _ = h.comm.Send(ctx, participant.PhysicalEndpoint, msg)
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
