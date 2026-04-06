package orchestrators

import (
	"context"
	"errors"
	"time"

	durableraft "github.com/AnishMulay/sandstore/internal/metadata_replicator/durable_raft"
	raft "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	"github.com/AnishMulay/sandstore/internal/metrics"
	"github.com/AnishMulay/sandstore/internal/orchestrators/protocol"
)

type consensusRPCHandler interface {
	HandleConsensusRequestVote(ctx context.Context, req raft.RequestVoteArgs) (*raft.RequestVoteReply, error)
	HandleConsensusAppendEntries(ctx context.Context, req protocol.AppendEntriesRequest) (*raft.AppendEntriesReply, error)
	HandleConsensusInstallSnapshot(ctx context.Context, req protocol.InstallSnapshotRequest) (*raft.InstallSnapshotReply, error)
}

type consensusHandler struct {
	replicator     *durableraft.DurableRaftReplicator
	metricsService metrics.MetricsService
}

func NewConsensusHandler(
	replicator *durableraft.DurableRaftReplicator,
	metricsService metrics.MetricsService,
) consensusRPCHandler {
	return &consensusHandler{
		replicator:     replicator,
		metricsService: metricsService,
	}
}

func (c *consensusHandler) HandleConsensusRequestVote(ctx context.Context, req raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	start := time.Now()
	defer func() {
		if c == nil || c.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		c.metricsService.Observe(metrics.ControlPlaneHandleConsensusRequestVoteLatency, elapsed, metrics.MetricTags{
			Operation: "handle_consensus_request_vote",
			Service:   "controlPlaneOrchestrator",
		})
	}()

	if c.replicator == nil {
		return nil, errors.New("consensus handler not configured")
	}

	return c.replicator.HandleRequestVote(ctx, req)
}

func (c *consensusHandler) HandleConsensusAppendEntries(ctx context.Context, req protocol.AppendEntriesRequest) (*raft.AppendEntriesReply, error) {
	start := time.Now()
	defer func() {
		if c == nil || c.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		c.metricsService.Observe(metrics.ControlPlaneHandleConsensusAppendEntriesLatency, elapsed, metrics.MetricTags{
			Operation: "handle_consensus_append_entries",
			Service:   "controlPlaneOrchestrator",
		})
	}()

	if c.replicator == nil {
		return nil, errors.New("consensus handler not configured")
	}

	return c.replicator.HandleAppendEntries(ctx, req)
}

func (c *consensusHandler) HandleConsensusInstallSnapshot(ctx context.Context, req protocol.InstallSnapshotRequest) (*raft.InstallSnapshotReply, error) {
	start := time.Now()
	defer func() {
		if c == nil || c.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		c.metricsService.Observe(metrics.ControlPlaneHandleConsensusInstallSnapshotLatency, elapsed, metrics.MetricTags{
			Operation: "handle_consensus_install_snapshot",
			Service:   "controlPlaneOrchestrator",
		})
	}()

	if c.replicator == nil {
		return nil, errors.New("consensus handler not configured")
	}

	return c.replicator.HandleInstallSnapshot(ctx, req)
}
