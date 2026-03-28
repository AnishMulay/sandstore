package durable_raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/metrics"
	ps "github.com/AnishMulay/sandstore/internal/server"
)

const heartbeatInterval = 50 * time.Millisecond

type DurableRaftReplicator struct {
	mu sync.RWMutex

	id             string
	clusterService cluster_service.ClusterService
	comm           communication.Communicator
	ls             log_service.LogService
	metricsService metrics.MetricsService
	applier        pmr.ApplyFunc

	config        RaftConfig
	logStore      LogStore
	stableStore   StableStore
	snapshotStore SnapshotStore
	stateMachine  pms.SnapshotableStateMachine

	state       raft_replicator.RaftState
	currentTerm uint64
	votedFor    string

	commitIndex uint64
	lastApplied uint64

	lastIncludedIndex uint64
	lastIncludedTerm  uint64

	nextIndex  map[string]uint64
	matchIndex map[string]uint64
	leaderID   string

	electionResetEvent time.Time
	stopCh             chan struct{}
	stopOnce           sync.Once

	pendingBatch   []raft_replicator.LogEntry
	pendingClients map[int64]chan error
}

func NewDurableRaftReplicator(
	id string,
	cs cluster_service.ClusterService,
	comm communication.Communicator,
	ls log_service.LogService,
	cfg RaftConfig,
	logStore LogStore,
	stableStore StableStore,
	snapshotStore SnapshotStore,
	metricsService metrics.MetricsService,
	stateMachine pms.SnapshotableStateMachine,
) *DurableRaftReplicator {
	return &DurableRaftReplicator{
		id:             id,
		clusterService: cs,
		comm:           comm,
		ls:             ls,
		metricsService: metricsService,
		config:         cfg,
		logStore:       logStore,
		stableStore:    stableStore,
		snapshotStore:  snapshotStore,
		stateMachine:   stateMachine,
		nextIndex:      make(map[string]uint64),
		matchIndex:     make(map[string]uint64),
		pendingClients: make(map[int64]chan error),
		stopCh:         make(chan struct{}),
	}
}

func (r *DurableRaftReplicator) Start(applier pmr.ApplyFunc) error {
	r.applier = applier

	term, votedFor, err := r.stableStore.GetState()
	if err != nil {
		return fmt.Errorf("load stable state: %w", err)
	}
	r.currentTerm = term
	r.votedFor = votedFor

	meta, snapshotData, err := r.snapshotStore.LoadSnapshot()
	if err != nil {
		return fmt.Errorf("load snapshot state: %w", err)
	}
	if meta.LastIncludedIndex > 0 {
		if len(snapshotData) == 0 {
			return fmt.Errorf("snapshot meta exists without snapshot data")
		}
		r.lastIncludedIndex = meta.LastIncludedIndex
		r.lastIncludedTerm = meta.LastIncludedTerm
		r.commitIndex = meta.LastIncludedIndex
		r.lastApplied = meta.LastIncludedIndex
	}

	lastIdx, _, err := r.logStore.LastIndexAndTerm()
	if err != nil {
		return fmt.Errorf("load log state: %w", err)
	}
	if lastIdx < r.lastIncludedIndex {
		return fmt.Errorf("wal behind snapshot: lastIdx=%d lastIncluded=%d", lastIdx, r.lastIncludedIndex)
	}

	r.state = raft_replicator.Follower
	r.electionResetEvent = time.Now()

	go r.runEventLoop()
	return nil
}

func (r *DurableRaftReplicator) Stop() error {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.mu.Lock()
		defer r.mu.Unlock()
		for idx, ch := range r.pendingClients {
			select {
			case ch <- context.Canceled:
			default:
			}
			close(ch)
			delete(r.pendingClients, idx)
		}
	})
	return nil
}

func (r *DurableRaftReplicator) Replicate(ctx context.Context, data []byte) error {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorReplicateLatency, elapsed, metrics.MetricTags{
			Operation: "replicate",
			Service:   "DurableRaftReplicator",
		})
	}()

	r.mu.Lock()
	if r.state != raft_replicator.Leader {
		leaderID := r.leaderID
		leaderAddr := r.findLeaderAddrLocked(leaderID)
		r.mu.Unlock()
		return raft_replicator.ErrNotLeader{LeaderID: leaderID, LeaderAddr: leaderAddr}
	}

	lastIdx, _, err := r.logStore.LastIndexAndTerm()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	entryIndex := int64(lastIdx) + int64(len(r.pendingBatch)) + 1

	entry := raft_replicator.LogEntry{
		Index:     entryIndex,
		Term:      int64(r.currentTerm),
		Data:      data,
		Timestamp: time.Now(),
	}
	r.pendingBatch = append(r.pendingBatch, entry)

	resultCh := make(chan error, 1)
	r.pendingClients[entry.Index] = resultCh

	if r.config.MaxBatchSize <= 1 || len(r.pendingBatch) >= r.config.MaxBatchSize {
		r.flushPendingBatchLocked()
	}
	r.mu.Unlock()

	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		r.mu.Lock()
		delete(r.pendingClients, entry.Index)
		r.mu.Unlock()
		return ctx.Err()
	case <-r.stopCh:
		return context.Canceled
	}
}

func (r *DurableRaftReplicator) runEventLoop() {
	electionTimeout := randomElectionTimeout()
	ticker := time.NewTicker(10 * time.Millisecond)
	batchWait := r.config.MaxBatchWaitTime
	if batchWait <= 0 {
		batchWait = 10 * time.Millisecond
	}
	batchTicker := time.NewTicker(batchWait)
	compactionTicker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	defer batchTicker.Stop()
	defer compactionTicker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-batchTicker.C:
			r.mu.Lock()
			if r.state == raft_replicator.Leader && len(r.pendingBatch) > 0 {
				r.flushPendingBatchLocked()
			}
			r.mu.Unlock()
		case <-ticker.C:
			r.mu.Lock()
			elapsed := time.Since(r.electionResetEvent)
			if r.state == raft_replicator.Leader {
				if elapsed >= heartbeatInterval {
					go r.broadcastAppendEntries()
					r.electionResetEvent = time.Now()
				}
			} else if elapsed >= electionTimeout {
				r.startElectionLocked()
				electionTimeout = randomElectionTimeout()
			}
			r.mu.Unlock()
		case <-compactionTicker.C:
			r.checkCompaction()
		}
	}
}

func (r *DurableRaftReplicator) flushPendingBatchLocked() {
	if len(r.pendingBatch) == 0 {
		return
	}

	batch := make([]raft_replicator.LogEntry, len(r.pendingBatch))
	copy(batch, r.pendingBatch)
	r.pendingBatch = nil

	if err := r.logStore.StoreLogs(batch); err != nil {
		for _, entry := range batch {
			if ch, ok := r.pendingClients[entry.Index]; ok {
				select {
				case ch <- err:
				default:
				}
				close(ch)
				delete(r.pendingClients, entry.Index)
			}
		}
		return
	}

	go r.broadcastAppendEntries()
}

func (r *DurableRaftReplicator) startElectionLocked() {
	r.state = raft_replicator.Candidate
	r.currentTerm++
	r.votedFor = r.id
	r.electionResetEvent = time.Now()
	if err := r.persistStableLocked(); err != nil {
		r.handleStableStoreFailureLocked("starting election", err)
		return
	}

	nodes, err := r.clusterService.GetAllNodes()
	if err != nil {
		r.ls.Error(log_service.LogEvent{Message: "failed to list nodes for election", Metadata: map[string]any{"error": err.Error()}})
		return
	}

	lastIdx, lastTerm := r.getLastLogInfoLocked()
	quorum := (len(nodes) / 2) + 1
	if len(nodes) == 1 {
		r.becomeLeaderLocked(nodes)
		return
	}

	savedTerm := r.currentTerm
	votesReceived := 1
	var voteMu sync.Mutex

	for _, n := range nodes {
		if n.ID == r.id {
			continue
		}

		peer := n
		go func() {
			args := raft_replicator.RequestVoteArgs{
				Term:         int64(savedTerm),
				CandidateID:  r.id,
				LastLogIndex: int64(lastIdx),
				LastLogTerm:  int64(lastTerm),
			}
			msg := communication.Message{
				From:    r.comm.Address(),
				Type:    ps.MsgRaftRequestVote,
				Payload: args,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			resp, err := r.comm.Send(ctx, peer.Address, msg)
			if err != nil || resp.Code != communication.CodeOK {
				return
			}

			var reply raft_replicator.RequestVoteReply
			if err := json.Unmarshal(resp.Body, &reply); err != nil {
				return
			}

			r.mu.Lock()
			defer r.mu.Unlock()
			if r.state != raft_replicator.Candidate || r.currentTerm != savedTerm {
				return
			}
			if reply.Term > int64(r.currentTerm) {
				if err := r.stepDownLocked(uint64(reply.Term), "", "received higher term in vote response"); err != nil {
					return
				}
				return
			}

			if reply.VoteGranted {
				voteMu.Lock()
				votesReceived++
				votes := votesReceived
				voteMu.Unlock()
				if votes >= quorum && r.state == raft_replicator.Candidate {
					r.becomeLeaderLocked(nodes)
				}
			}
		}()
	}
}

func (r *DurableRaftReplicator) becomeLeaderLocked(nodes []cluster_service.Node) {
	r.state = raft_replicator.Leader
	r.leaderID = r.id
	lastIdx, _ := r.getLastLogInfoLocked()

	r.ls.Info(log_service.LogEvent{
		Message:  "Became Leader",
		Metadata: map[string]any{"nodeID": r.id, "term": r.currentTerm},
	})

	for _, n := range nodes {
		r.nextIndex[n.ID] = lastIdx + 1
		r.matchIndex[n.ID] = r.lastIncludedIndex
	}
	r.matchIndex[r.id] = lastIdx

	if len(r.pendingBatch) > 0 {
		r.flushPendingBatchLocked()
	}
	go r.broadcastAppendEntries()
}

type appendWork struct {
	node         cluster_service.Node
	prevLogIndex int64
	prevLogTerm  int64
	entries      []raft_replicator.LogEntry
	term         uint64
	commitIndex  uint64
}

type snapshotWork struct {
	node cluster_service.Node
	req  communication.InstallSnapshotRequest
	term uint64
}

func (r *DurableRaftReplicator) broadcastAppendEntries() {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorBroadcastAppendEntriesLatency, elapsed, metrics.MetricTags{
			Operation: "broadcast_append_entries",
			Service:   "DurableRaftReplicator",
		})
	}()

	r.mu.Lock()
	if r.state != raft_replicator.Leader {
		r.mu.Unlock()
		return
	}

	savedTerm := r.currentTerm
	savedCommitIndex := r.commitIndex
	nodes, err := r.clusterService.GetAllNodes()
	if err != nil {
		r.mu.Unlock()
		return
	}
	lastIdx, _ := r.getLastLogInfoLocked()

	var appendWorkList []appendWork
	var snapshotWorkList []snapshotWork

	snapshotMeta, snapshotData, snapshotErr := r.snapshotStore.LoadSnapshot()
	if snapshotErr != nil {
		r.mu.Unlock()
		r.ls.Error(log_service.LogEvent{Message: "failed to load snapshot for replication", Metadata: map[string]any{"error": snapshotErr.Error()}})
		return
	}

	for _, node := range nodes {
		if node.ID == r.id {
			continue
		}

		nextIdx := r.nextIndex[node.ID]
		if nextIdx == 0 {
			nextIdx = r.lastIncludedIndex + 1
		}

		if nextIdx <= r.lastIncludedIndex {
			if snapshotMeta.LastIncludedIndex == 0 || len(snapshotData) == 0 {
				continue
			}
			snapshotWorkList = append(snapshotWorkList, snapshotWork{
				node: node,
				req: communication.InstallSnapshotRequest{
					Term:              int64(savedTerm),
					LeaderID:          r.id,
					LastIncludedIndex: int64(snapshotMeta.LastIncludedIndex),
					LastIncludedTerm:  int64(snapshotMeta.LastIncludedTerm),
					Data:              snapshotData,
				},
				term: savedTerm,
			})
			continue
		}

		prevLogIndex := nextIdx - 1
		prevLogTerm := int64(0)
		if prevLogIndex == r.lastIncludedIndex {
			prevLogTerm = int64(r.lastIncludedTerm)
		} else if prevLogIndex > 0 {
			entry, err := r.logStore.GetLog(prevLogIndex)
			if err != nil {
				if errors.Is(err, ErrLogCompacted) && snapshotMeta.LastIncludedIndex > 0 {
					snapshotWorkList = append(snapshotWorkList, snapshotWork{
						node: node,
						req: communication.InstallSnapshotRequest{
							Term:              int64(savedTerm),
							LeaderID:          r.id,
							LastIncludedIndex: int64(snapshotMeta.LastIncludedIndex),
							LastIncludedTerm:  int64(snapshotMeta.LastIncludedTerm),
							Data:              snapshotData,
						},
						term: savedTerm,
					})
					continue
				}
				continue
			}
			prevLogTerm = entry.Term
		}

		entries := make([]raft_replicator.LogEntry, 0)
		if nextIdx <= lastIdx {
			for idx := nextIdx; idx <= lastIdx; idx++ {
				entry, err := r.logStore.GetLog(idx)
				if err != nil {
					break
				}
				entries = append(entries, entry)
			}
		}

		appendWorkList = append(appendWorkList, appendWork{
			node:         node,
			prevLogIndex: int64(prevLogIndex),
			prevLogTerm:  prevLogTerm,
			entries:      entries,
			term:         savedTerm,
			commitIndex:  savedCommitIndex,
		})
	}
	r.mu.Unlock()

	for _, work := range appendWorkList {
		w := work
		go r.replicateAppendEntries(w)
	}
	for _, work := range snapshotWorkList {
		w := work
		go r.replicateSnapshot(w)
	}
}

func (r *DurableRaftReplicator) replicateAppendEntries(w appendWork) {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorReplicateAppendEntriesLatency, elapsed, metrics.MetricTags{
			Operation: "replicate_append_entries",
			Service:   "DurableRaftReplicator",
		})
	}()

	entriesBytes, err := json.Marshal(w.entries)
	if err != nil {
		return
	}

	args := communication.AppendEntriesRequest{
		Term:         int64(w.term),
		LeaderID:     r.id,
		PrevLogIndex: w.prevLogIndex,
		PrevLogTerm:  w.prevLogTerm,
		Entries:      entriesBytes,
		LeaderCommit: int64(w.commitIndex),
	}
	msg := communication.Message{
		From:    r.comm.Address(),
		Type:    ps.MsgRaftAppendEntries,
		Payload: args,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := r.comm.Send(ctx, w.node.Address, msg)
	if err != nil || resp.Code != communication.CodeOK {
		return
	}

	var reply raft_replicator.AppendEntriesReply
	if err := json.Unmarshal(resp.Body, &reply); err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != raft_replicator.Leader || r.currentTerm != w.term {
		return
	}
	if reply.Term > int64(r.currentTerm) {
		if err := r.stepDownLocked(uint64(reply.Term), "", "received higher term in append entries response"); err != nil {
			return
		}
		return
	}

	if reply.Success {
		newMatchIndex := uint64(w.prevLogIndex)
		if len(w.entries) > 0 {
			newMatchIndex = uint64(w.entries[len(w.entries)-1].Index)
		}
		if newMatchIndex > r.matchIndex[w.node.ID] {
			r.matchIndex[w.node.ID] = newMatchIndex
			r.nextIndex[w.node.ID] = newMatchIndex + 1
			r.advanceCommitIndexLocked()
		}
		return
	}

	if reply.ConflictIndex > 0 {
		r.nextIndex[w.node.ID] = uint64(reply.ConflictIndex)
		if r.nextIndex[w.node.ID] < 1 {
			r.nextIndex[w.node.ID] = 1
		}
		return
	}

	if r.nextIndex[w.node.ID] > 1 {
		r.nextIndex[w.node.ID]--
	}
}

func (r *DurableRaftReplicator) replicateSnapshot(w snapshotWork) {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorReplicateSnapshotLatency, elapsed, metrics.MetricTags{
			Operation: "replicate_snapshot",
			Service:   "DurableRaftReplicator",
		})
	}()

	msg := communication.Message{
		From:    r.comm.Address(),
		Type:    ps.MsgRaftInstallSnapshot,
		Payload: w.req,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := r.comm.Send(ctx, w.node.Address, msg)
	if err != nil || resp.Code != communication.CodeOK {
		return
	}

	var reply raft_replicator.InstallSnapshotReply
	if err := json.Unmarshal(resp.Body, &reply); err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != raft_replicator.Leader || r.currentTerm != w.term {
		return
	}
	if reply.Term > int64(r.currentTerm) {
		if err := r.stepDownLocked(uint64(reply.Term), "", "received higher term in install snapshot response"); err != nil {
			return
		}
		return
	}
	if reply.Success {
		idx := uint64(w.req.LastIncludedIndex)
		r.matchIndex[w.node.ID] = idx
		r.nextIndex[w.node.ID] = idx + 1
	}
}

func (r *DurableRaftReplicator) advanceCommitIndexLocked() {
	lastIdx, _, err := r.logStore.LastIndexAndTerm()
	if err != nil {
		return
	}

	nodes, err := r.clusterService.GetAllNodes()
	if err != nil || len(nodes) == 0 {
		return
	}

	for n := lastIdx; n > r.commitIndex; n-- {
		entry, err := r.logStore.GetLog(n)
		if err != nil {
			continue
		}
		if entry.Term != int64(r.currentTerm) {
			continue
		}

		matchCount := 1
		for _, node := range nodes {
			if node.ID == r.id {
				continue
			}
			if r.matchIndex[node.ID] >= n {
				matchCount++
			}
		}
		if matchCount > len(nodes)/2 {
			r.commitIndex = n
			r.applyLogsLocked()
			return
		}
	}
}

func (r *DurableRaftReplicator) applyLogsLocked() {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorApplyLogsLockedLatency, elapsed, metrics.MetricTags{
			Operation: "apply_logs_locked",
			Service:   "DurableRaftReplicator",
		})
	}()

	for r.lastApplied < r.commitIndex {
		nextIndex := r.lastApplied + 1
		if nextIndex <= r.lastIncludedIndex {
			r.lastApplied = nextIndex
			continue
		}

		entry, err := r.logStore.GetLog(nextIndex)
		if err != nil {
			return
		}

		r.lastApplied = nextIndex
		applyErr := r.applier(entry.Data)
		if applyErr != nil {
			r.ls.Error(log_service.LogEvent{Message: "failed to apply log entry", Metadata: map[string]any{"index": entry.Index, "error": applyErr.Error()}})
		}

		if ch, ok := r.pendingClients[entry.Index]; ok {
			select {
			case ch <- applyErr:
			default:
			}
			close(ch)
			delete(r.pendingClients, entry.Index)
		}
	}
}

func (r *DurableRaftReplicator) HandleAppendEntries(ctx context.Context, req communication.AppendEntriesRequest) (*raft_replicator.AppendEntriesReply, error) {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorHandleAppendEntriesLatency, elapsed, metrics.MetricTags{
			Operation: "handle_append_entries",
			Service:   "DurableRaftReplicator",
		})
	}()

	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()

	reply := &raft_replicator.AppendEntriesReply{
		Term:    int64(r.currentTerm),
		Success: false,
	}

	if req.Term < int64(r.currentTerm) {
		return reply, nil
	}

	if req.Term > int64(r.currentTerm) {
		if err := r.stepDownLocked(uint64(req.Term), req.LeaderID, "received higher term append entries"); err != nil {
			return reply, err
		}
	}

	wasFollower := r.state == raft_replicator.Follower
	r.state = raft_replicator.Follower
	r.leaderID = req.LeaderID
	r.electionResetEvent = time.Now()
	if !wasFollower {
		r.logFollowerTransitionLocked("received append entries from leader")
	}
	reply.Term = int64(r.currentTerm)

	if uint64(req.PrevLogIndex) < r.lastIncludedIndex {
		reply.ConflictIndex = int64(r.lastIncludedIndex + 1)
		return reply, nil
	}

	if req.PrevLogIndex == int64(r.lastIncludedIndex) {
		if req.PrevLogTerm != int64(r.lastIncludedTerm) {
			reply.ConflictIndex = int64(r.lastIncludedIndex)
			reply.ConflictTerm = int64(r.lastIncludedTerm)
			return reply, nil
		}
	} else if req.PrevLogIndex > 0 {
		prevEntry, err := r.logStore.GetLog(uint64(req.PrevLogIndex))
		if err != nil {
			lastIdx, _, _ := r.logStore.LastIndexAndTerm()
			reply.ConflictIndex = int64(lastIdx + 1)
			return reply, nil
		}
		if prevEntry.Term != req.PrevLogTerm {
			reply.ConflictTerm = prevEntry.Term
			reply.ConflictIndex = req.PrevLogIndex
			return reply, nil
		}
	}

	var entries []raft_replicator.LogEntry
	if len(req.Entries) > 0 {
		if err := json.Unmarshal(req.Entries, &entries); err != nil {
			return reply, nil
		}
	}

	var newLogs []raft_replicator.LogEntry
	for i, entry := range entries {
		if uint64(entry.Index) <= r.lastIncludedIndex {
			continue
		}

		existing, err := r.logStore.GetLog(uint64(entry.Index))
		if err == nil {
			if existing.Term != entry.Term {
				if err := r.logStore.DeleteRange(uint64(entry.Index), math.MaxUint64); err != nil {
					return reply, nil
				}
				newLogs = entries[i:]
				break
			}
			continue
		}

		if errors.Is(err, ErrLogNotFound) || errors.Is(err, ErrLogCompacted) {
			newLogs = entries[i:]
			break
		}
	}

	if len(newLogs) > 0 {
		if err := r.logStore.StoreLogs(newLogs); err != nil {
			return reply, nil
		}
	}

	lastIdx, _, err := r.logStore.LastIndexAndTerm()
	if err != nil {
		return reply, nil
	}

	if uint64(req.LeaderCommit) > r.commitIndex {
		targetCommit := uint64(req.LeaderCommit)
		if targetCommit > lastIdx {
			targetCommit = lastIdx
		}
		if targetCommit < r.lastIncludedIndex {
			targetCommit = r.lastIncludedIndex
		}
		r.commitIndex = targetCommit
		r.applyLogsLocked()
	}

	reply.Success = true
	return reply, nil
}

func (r *DurableRaftReplicator) HandleRequestVote(ctx context.Context, req raft_replicator.RequestVoteArgs) (*raft_replicator.RequestVoteReply, error) {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorHandleRequestVoteLatency, elapsed, metrics.MetricTags{
			Operation: "handle_request_vote",
			Service:   "DurableRaftReplicator",
		})
	}()

	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()

	reply := &raft_replicator.RequestVoteReply{
		Term:        int64(r.currentTerm),
		VoteGranted: false,
	}

	if req.Term < int64(r.currentTerm) {
		return reply, nil
	}
	if req.Term > int64(r.currentTerm) {
		if err := r.stepDownLocked(uint64(req.Term), "", "received higher term vote request"); err != nil {
			reply.Term = int64(r.currentTerm)
			return reply, err
		}
	}

	lastIdx, lastTerm := r.getLastLogInfoLocked()
	logIsUpToDate := req.LastLogTerm > int64(lastTerm) || (req.LastLogTerm == int64(lastTerm) && req.LastLogIndex >= int64(lastIdx))

	if (r.votedFor == "" || r.votedFor == req.CandidateID) && logIsUpToDate {
		r.votedFor = req.CandidateID
		if err := r.persistStableLocked(); err != nil {
			r.handleStableStoreFailureLocked("persisting granted vote", err)
			reply.Term = int64(r.currentTerm)
			return reply, err
		}
		r.electionResetEvent = time.Now()
		reply.VoteGranted = true
	}

	reply.Term = int64(r.currentTerm)
	return reply, nil
}

func (r *DurableRaftReplicator) HandleInstallSnapshot(ctx context.Context, req communication.InstallSnapshotRequest) (*raft_replicator.InstallSnapshotReply, error) {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorHandleInstallSnapshotLatency, elapsed, metrics.MetricTags{
			Operation: "handle_install_snapshot",
			Service:   "DurableRaftReplicator",
		})
	}()

	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()

	reply := &raft_replicator.InstallSnapshotReply{
		Term:    int64(r.currentTerm),
		Success: false,
	}

	if req.Term < int64(r.currentTerm) {
		return reply, nil
	}
	if req.Term > int64(r.currentTerm) {
		if err := r.stepDownLocked(uint64(req.Term), req.LeaderID, "received higher term install snapshot"); err != nil {
			return reply, err
		}
	}

	wasFollower := r.state == raft_replicator.Follower
	r.state = raft_replicator.Follower
	r.leaderID = req.LeaderID
	r.electionResetEvent = time.Now()
	if !wasFollower {
		r.logFollowerTransitionLocked("received install snapshot from leader")
	}
	reply.Term = int64(r.currentTerm)

	incomingIndex := uint64(req.LastIncludedIndex)
	incomingTerm := uint64(req.LastIncludedTerm)
	if incomingIndex <= r.lastIncludedIndex {
		reply.Success = true
		return reply, nil
	}

	retainSuffix := false
	if entry, err := r.logStore.GetLog(incomingIndex); err == nil && uint64(entry.Term) == incomingTerm {
		retainSuffix = true
	}

	meta := SnapshotMeta{LastIncludedIndex: incomingIndex, LastIncludedTerm: incomingTerm}
	if err := r.snapshotStore.SaveSnapshot(meta, req.Data); err != nil {
		return reply, nil
	}

	if retainSuffix {
		if err := r.logStore.DeleteRange(0, incomingIndex); err != nil {
			return reply, nil
		}
	} else {
		lastIdx, _, err := r.logStore.LastIndexAndTerm()
		if err != nil {
			return reply, nil
		}
		if err := r.logStore.DeleteRange(0, lastIdx); err != nil {
			return reply, nil
		}
		if err := r.stateMachine.RestoreSnapshot(req.Data); err != nil {
			return reply, nil
		}
	}

	r.lastIncludedIndex = incomingIndex
	r.lastIncludedTerm = incomingTerm
	if r.commitIndex < incomingIndex {
		r.commitIndex = incomingIndex
	}
	if r.lastApplied < incomingIndex {
		r.lastApplied = incomingIndex
	}

	reply.Success = true
	return reply, nil
}

func (r *DurableRaftReplicator) checkCompaction() {
	start := time.Now()
	defer func() {
		if r == nil || r.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		r.metricsService.Observe(metrics.RaftReplicatorCheckCompactionLatency, elapsed, metrics.MetricTags{
			Operation: "check_compaction",
			Service:   "DurableRaftReplicator",
		})
	}()

	if r.config.SnapshotThresholdLogs == 0 {
		return
	}

	r.mu.Lock()
	if r.lastApplied <= r.lastIncludedIndex {
		r.mu.Unlock()
		return
	}
	if r.lastApplied-r.lastIncludedIndex < r.config.SnapshotThresholdLogs {
		r.mu.Unlock()
		return
	}

	snapshotIndex := r.lastApplied
	snapshotTerm := r.lastIncludedTerm
	if snapshotIndex > r.lastIncludedIndex {
		if entry, err := r.logStore.GetLog(snapshotIndex); err == nil {
			snapshotTerm = uint64(entry.Term)
		}
	}
	r.mu.Unlock()

	snapshotData, err := r.stateMachine.SerializeSnapshot()
	if err != nil {
		r.ls.Error(log_service.LogEvent{Message: "state machine snapshot failed", Metadata: map[string]any{"error": err.Error()}})
		return
	}

	meta := SnapshotMeta{LastIncludedIndex: snapshotIndex, LastIncludedTerm: snapshotTerm}
	if err := r.snapshotStore.SaveSnapshot(meta, snapshotData); err != nil {
		r.ls.Error(log_service.LogEvent{Message: "snapshot store write failed", Metadata: map[string]any{"error": err.Error()}})
		return
	}

	if err := r.logStore.DeleteRange(0, snapshotIndex); err != nil {
		r.ls.Error(log_service.LogEvent{Message: "wal compaction failed", Metadata: map[string]any{"error": err.Error()}})
		return
	}

	r.mu.Lock()
	if snapshotIndex > r.lastIncludedIndex {
		r.lastIncludedIndex = snapshotIndex
		r.lastIncludedTerm = snapshotTerm
	}
	r.mu.Unlock()
}

func (r *DurableRaftReplicator) findLeaderAddrLocked(leaderID string) string {
	if leaderID == "" {
		return ""
	}
	nodes, err := r.clusterService.GetAllNodes()
	if err != nil {
		return ""
	}
	for _, n := range nodes {
		if n.ID == leaderID {
			return n.Address
		}
	}
	return ""
}

func (r *DurableRaftReplicator) GetLeaderAddress() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.findLeaderAddrLocked(r.leaderID)
}

func (r *DurableRaftReplicator) getLastLogInfoLocked() (uint64, uint64) {
	idx, term, err := r.logStore.LastIndexAndTerm()
	if err != nil {
		return r.lastIncludedIndex, r.lastIncludedTerm
	}
	if idx == r.lastIncludedIndex && term == 0 {
		return r.lastIncludedIndex, r.lastIncludedTerm
	}
	return idx, term
}

func (r *DurableRaftReplicator) stepDownLocked(newTerm uint64, leaderID string, reason string) error {
	r.state = raft_replicator.Follower
	r.currentTerm = newTerm
	r.votedFor = ""
	if leaderID != "" {
		r.leaderID = leaderID
	}
	r.failAllPendingLocked(fmt.Errorf("leadership changed during replication"))
	r.electionResetEvent = time.Now()
	if err := r.persistStableLocked(); err != nil {
		return r.handleStableStoreFailureLocked(reason, err)
	}
	r.logFollowerTransitionLocked(reason)
	return nil
}

// persistStableLocked writes currentTerm and votedFor to the stable store.
// Must be called with r.mu held.
// If the write fails, the caller must step the node down to follower state
// and reject any pending operations — this is a data safety violation.
func (r *DurableRaftReplicator) persistStableLocked() error {
	if err := r.stableStore.SetState(r.currentTerm, r.votedFor); err != nil {
		return fmt.Errorf("stable store write failed (term=%d): %w", r.currentTerm, err)
	}
	return nil
}

func randomElectionTimeout() time.Duration {
	return time.Duration(300+rand.Intn(300)) * time.Millisecond
}

func (r *DurableRaftReplicator) failAllPendingLocked(err error) {
	r.pendingBatch = nil
	for idx, ch := range r.pendingClients {
		select {
		case ch <- err:
		default:
		}
		close(ch)
		delete(r.pendingClients, idx)
	}
}

func (r *DurableRaftReplicator) handleStableStoreFailureLocked(reason string, err error) error {
	r.ls.Error(log_service.LogEvent{
		Message: "stable store persistence failed",
		Metadata: map[string]any{
			"nodeID": r.id,
			"term":   r.currentTerm,
			"reason": reason,
			"error":  err.Error(),
		},
	})

	r.state = raft_replicator.Follower
	r.electionResetEvent = time.Now()
	r.failAllPendingLocked(fmt.Errorf("stable store persistence failure: %w", err))
	r.logFollowerTransitionLocked("stable store failure")
	return err
}

func (r *DurableRaftReplicator) logFollowerTransitionLocked(reason string) {
	r.ls.Info(log_service.LogEvent{
		Message: "Became Follower",
		Metadata: map[string]any{
			"nodeID":   r.id,
			"term":     r.currentTerm,
			"reason":   reason,
			"newState": "Follower",
		},
	})
}
