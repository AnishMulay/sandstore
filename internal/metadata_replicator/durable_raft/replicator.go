package durable_raft

import (
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
)

type DurableRaftReplicator struct {
	mu sync.Mutex

	id             string
	clusterService cluster_service.ClusterService
	comm           communication.Communicator
	ls             log_service.LogService
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

	nextIndex  map[string]uint64
	matchIndex map[string]uint64
	leaderID   string

	electionResetEvent time.Time
	stopCh             chan struct{}

	pendingBatch   []raft_replicator.LogEntry
	pendingClients map[int64]chan error
	firstBatchTime time.Time
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
	stateMachine pms.SnapshotableStateMachine,
) *DurableRaftReplicator {
	return &DurableRaftReplicator{
		id:             id,
		clusterService: cs,
		comm:           comm,
		ls:             ls,
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

	// Flow 3: Crash Recovery and Initialization
	term, votedFor, err := r.stableStore.GetState()
	if err != nil {
		r.ls.Error(log_service.LogEvent{Message: "failed to load stable state, starting fresh"})
	} else {
		r.currentTerm = term
		r.votedFor = votedFor
	}

	_, _, err = r.logStore.LastIndexAndTerm()
	if err != nil {
		r.ls.Error(log_service.LogEvent{Message: "failed to load log state"})
	}

	meta, _, err := r.snapshotStore.LoadSnapshot()
	if err == nil && meta.LastIncludedIndex > 0 {
		r.commitIndex = meta.LastIncludedIndex
		r.lastApplied = meta.LastIncludedIndex
	} else {
		r.commitIndex = 0
		r.lastApplied = 0
	}

	r.state = raft_replicator.Follower
	r.electionResetEvent = time.Now()

	go r.runEventLoop()
	return nil
}

func (r *DurableRaftReplicator) Stop() error {
	close(r.stopCh)
	return nil
}

// Flow 1: Leader Batching & Fsync (Step 1)
func (r *DurableRaftReplicator) Replicate(ctx context.Context, data []byte) error {
	r.mu.Lock()
	if r.state != raft_replicator.Leader {
		leaderID := r.leaderID
		r.mu.Unlock()
		return raft_replicator.ErrNotLeader{LeaderID: leaderID, LeaderAddr: ""}
	}

	lastIdx, _, _ := r.logStore.LastIndexAndTerm()
	entryIndex := int64(lastIdx) + int64(len(r.pendingBatch)) + 1

	entry := raft_replicator.LogEntry{
		Index:     entryIndex,
		Term:      int64(r.currentTerm),
		Data:      data,
		Timestamp: time.Now(),
	}

	r.pendingBatch = append(r.pendingBatch, entry)
	if len(r.pendingBatch) == 1 {
		r.firstBatchTime = time.Now()
	}

	resultCh := make(chan error, 1)
	r.pendingClients[entry.Index] = resultCh

	// Step 2: BATCHING TRIGGER
	if len(r.pendingBatch) >= r.config.MaxBatchSize {
		r.flushPendingBatch() // Flushes inside lock
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
		return nil
	}
}

// Event Loop
func (r *DurableRaftReplicator) runEventLoop() {
	electionTimeout := randomElectionTimeout()
	ticker := time.NewTicker(10 * time.Millisecond)
	batchTicker := time.NewTicker(r.config.MaxBatchWaitTime)
	defer ticker.Stop()
	defer batchTicker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-batchTicker.C:
			r.mu.Lock()
			if r.state == raft_replicator.Leader && len(r.pendingBatch) > 0 {
				r.flushPendingBatch()
			}
			r.mu.Unlock()
		case <-ticker.C:
			r.mu.Lock()
			elapsed := time.Since(r.electionResetEvent)

			if r.state != raft_replicator.Leader && elapsed >= electionTimeout {
				r.startElection()
				electionTimeout = randomElectionTimeout()
				r.electionResetEvent = time.Now()
			}

			if r.state == raft_replicator.Leader {
				if elapsed >= 50*time.Millisecond {
					r.broadcastAppendEntries()
					r.electionResetEvent = time.Now()
				}
			}
			r.mu.Unlock()

			r.checkCompaction()
		}
	}
}

// flushPendingBatch assumes r.mu is locked
func (r *DurableRaftReplicator) flushPendingBatch() {
	if len(r.pendingBatch) == 0 {
		return
	}

	// Step 3: DURABLE PERSISTENCE (FSYNC)
	err := r.logStore.StoreLogs(r.pendingBatch)
	if err != nil {
		for _, entry := range r.pendingBatch {
			if ch, ok := r.pendingClients[entry.Index]; ok {
				ch <- err
				delete(r.pendingClients, entry.Index)
			}
		}
		r.pendingBatch = nil
		return
	}

	// Logs are stored locally.
	r.pendingBatch = nil
	// Step 4: BROADCAST
	go r.broadcastAppendEntries()
}

// Basic Election logic (skeleton)
func (r *DurableRaftReplicator) startElection() {
	r.state = raft_replicator.Candidate
	r.currentTerm++
	r.votedFor = r.id

	_ = r.stableStore.SetState(r.currentTerm, r.votedFor)

	nodes, _ := r.clusterService.GetAllNodes()
	lastIdx, lastTerm, _ := r.logStore.LastIndexAndTerm()
	quorum := (len(nodes) / 2) + 1

	if len(nodes) == 1 {
		r.becomeLeader()
		return
	}

	var votesReceived int = 1
	var voteMu sync.Mutex

	for _, n := range nodes {
		if n.ID == r.id {
			continue
		}
		go func(peer cluster_service.Node) {
			args := raft_replicator.RequestVoteArgs{
				Term:         int64(r.currentTerm),
				CandidateID:  r.id,
				LastLogIndex: int64(lastIdx),
				LastLogTerm:  int64(lastTerm),
			}
			msg := communication.Message{
				From:    r.comm.Address(),
				Type:    "raft_request_vote",
				Payload: args,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			resp, err := r.comm.Send(ctx, peer.Address, msg)
			if err != nil || resp.Code != communication.CodeOK {
				return
			}
			var reply raft_replicator.RequestVoteReply
			if json.Unmarshal(resp.Body, &reply) != nil {
				return
			}

			r.mu.Lock()
			defer r.mu.Unlock()
			if r.state != raft_replicator.Candidate || r.currentTerm != uint64(args.Term) {
				return
			}
			if reply.Term > int64(r.currentTerm) {
				r.state = raft_replicator.Follower
				r.currentTerm = uint64(reply.Term)
				r.votedFor = ""
				_ = r.stableStore.SetState(r.currentTerm, "")
				return
			}
			if reply.VoteGranted {
				voteMu.Lock()
				votesReceived++
				v := votesReceived
				voteMu.Unlock()
				if v >= quorum && r.state == raft_replicator.Candidate {
					r.becomeLeader()
				}
			}
		}(n)
	}
}

func (r *DurableRaftReplicator) becomeLeader() {
	r.state = raft_replicator.Leader
	nodes, _ := r.clusterService.GetAllNodes()
	lastIdx, _, _ := r.logStore.LastIndexAndTerm()
	for _, n := range nodes {
		r.nextIndex[n.ID] = lastIdx + 1
		r.matchIndex[n.ID] = 0
	}
	r.leaderID = r.id
	r.flushPendingBatch()
	go r.broadcastAppendEntries()
}

func (r *DurableRaftReplicator) broadcastAppendEntries() {
	r.mu.Lock()
	if r.state != raft_replicator.Leader {
		r.mu.Unlock()
		return
	}
	savedTerm := r.currentTerm
	savedCommitIndex := r.commitIndex
	leaderID := r.id

	nodes, _ := r.clusterService.GetAllNodes()
	type peerState struct {
		node         cluster_service.Node
		nextIndex    int64
		prevLogIndex int64
		prevLogTerm  int64
		entries      []raft_replicator.LogEntry
	}

	var workList []peerState
	lastIdx, _, _ := r.logStore.LastIndexAndTerm()

	for _, node := range nodes {
		if node.ID == r.id {
			continue
		}
		nextIdx := r.nextIndex[node.ID]
		if nextIdx == 0 {
			nextIdx = 1
		}

		prevLogIndex := nextIdx - 1
		var prevLogTerm int64
		if prevLogIndex > 0 {
			if entry, err := r.logStore.GetLog(prevLogIndex); err == nil {
				prevLogTerm = entry.Term
			} else {
				// Snapshot logic goes here later
			}
		}

		var entries []raft_replicator.LogEntry
		if nextIdx <= lastIdx {
			for i := nextIdx; i <= lastIdx; i++ {
				if entry, err := r.logStore.GetLog(i); err == nil {
					entries = append(entries, entry)
				}
			}
		}

		workList = append(workList, peerState{
			node:         node,
			nextIndex:    int64(nextIdx),
			prevLogIndex: int64(prevLogIndex),
			prevLogTerm:  prevLogTerm,
			entries:      entries,
		})
	}
	r.mu.Unlock()

	for _, work := range workList {
		go func(w peerState) {
			entriesBytes, _ := json.Marshal(w.entries)
			args := communication.AppendEntriesRequest{
				Term:         int64(savedTerm),
				LeaderID:     leaderID,
				PrevLogIndex: w.prevLogIndex,
				PrevLogTerm:  w.prevLogTerm,
				Entries:      entriesBytes,
				LeaderCommit: int64(savedCommitIndex),
			}
			msg := communication.Message{
				From:    r.comm.Address(),
				Type:    "raft_append_entries",
				Payload: args,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := r.comm.Send(ctx, w.node.Address, msg)
			if err != nil || resp.Code != communication.CodeOK {
				return
			}

			var reply raft_replicator.AppendEntriesReply
			if json.Unmarshal(resp.Body, &reply) != nil {
				return
			}

			r.mu.Lock()
			defer r.mu.Unlock()

			if r.state != raft_replicator.Leader || r.currentTerm != savedTerm {
				return
			}

			if reply.Term > int64(r.currentTerm) {
				r.state = raft_replicator.Follower
				r.currentTerm = uint64(reply.Term)
				r.votedFor = ""
				_ = r.stableStore.SetState(r.currentTerm, "")
				return
			}

			if reply.Success {
				newMatchIndex := uint64(w.prevLogIndex + int64(len(w.entries)))
				if newMatchIndex > r.matchIndex[w.node.ID] {
					r.matchIndex[w.node.ID] = newMatchIndex
					r.nextIndex[w.node.ID] = newMatchIndex + 1
					r.advanceCommitIndex()
				}
			} else {
				newNext := r.nextIndex[w.node.ID] - 1
				if newNext < 1 {
					newNext = 1
				}
				r.nextIndex[w.node.ID] = newNext
				if reply.ConflictIndex > 0 {
					r.nextIndex[w.node.ID] = uint64(reply.ConflictIndex)
				}
			}
		}(work)
	}
}

func (r *DurableRaftReplicator) advanceCommitIndex() {
	lastIdx, _, _ := r.logStore.LastIndexAndTerm()
	for n := lastIdx; n > r.commitIndex; n-- {
		entry, err := r.logStore.GetLog(n)
		if err != nil || entry.Term != int64(r.currentTerm) {
			continue
		}
		matchCount := 1
		nodes, _ := r.clusterService.GetAllNodes()
		for _, node := range nodes {
			if node.ID != r.id && r.matchIndex[node.ID] >= n {
				matchCount++
			}
		}
		if matchCount > len(nodes)/2 {
			r.commitIndex = n
			r.applyLogs()
			break
		}
	}
}

func (r *DurableRaftReplicator) applyLogs() {
	for r.commitIndex > r.lastApplied {
		r.lastApplied++
		entry, err := r.logStore.GetLog(r.lastApplied)
		if err == nil {
			err = r.applier(entry.Data)
			if ch, ok := r.pendingClients[entry.Index]; ok {
				ch <- err
				delete(r.pendingClients, entry.Index)
			}
		}
	}
}

// Flow 2: Follower AppendEntries
func (r *DurableRaftReplicator) HandleAppendEntries(req communication.AppendEntriesRequest) (*raft_replicator.AppendEntriesReply, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reply := &raft_replicator.AppendEntriesReply{
		Term:    int64(r.currentTerm),
		Success: false,
	}

	var entries []raft_replicator.LogEntry
	if len(req.Entries) > 0 {
		_ = json.Unmarshal(req.Entries, &entries)
	}

	if req.Term > int64(r.currentTerm) {
		r.state = raft_replicator.Follower
		r.currentTerm = uint64(req.Term)
		r.votedFor = ""
		_ = r.stableStore.SetState(r.currentTerm, "")
	}

	if req.Term < int64(r.currentTerm) {
		return reply, nil
	}

	r.electionResetEvent = time.Now()
	r.leaderID = req.LeaderID

	// Log Consistency
	if req.PrevLogIndex > 0 {
		lastIdx, _, _ := r.logStore.LastIndexAndTerm()
		if uint64(req.PrevLogIndex) > lastIdx {
			reply.ConflictIndex = int64(lastIdx + 1)
			return reply, nil
		}
		entry, err := r.logStore.GetLog(uint64(req.PrevLogIndex))
		if err == nil && entry.Term != req.PrevLogTerm {
			reply.ConflictTerm = entry.Term
			return reply, nil
		}
	}

	// Conflict resolution & truncate
	var newLogs []raft_replicator.LogEntry
	for i, entry := range entries {
		existing, err := r.logStore.GetLog(uint64(entry.Index))
		if err == nil && existing.Term != entry.Term {
			_ = r.logStore.DeleteRange(uint64(entry.Index), ^uint64(0)) // Delete rest
			newLogs = entries[i:]
			break
		} else if err != nil { // Doesn't exist
			newLogs = entries[i:]
			break
		}
	}

	if len(newLogs) > 0 {
		if err := r.logStore.StoreLogs(newLogs); err != nil {
			return reply, nil // Return failure
		}
	}

	lastNewIdx := uint64(req.PrevLogIndex) + uint64(len(entries))
	if uint64(req.LeaderCommit) > r.commitIndex {
		if uint64(req.LeaderCommit) < lastNewIdx {
			r.commitIndex = uint64(req.LeaderCommit)
		} else {
			r.commitIndex = lastNewIdx
		}
		r.applyLogs()
	}

	reply.Success = true
	return reply, nil
}

func (r *DurableRaftReplicator) HandleRequestVote(req raft_replicator.RequestVoteArgs) (*raft_replicator.RequestVoteReply, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reply := &raft_replicator.RequestVoteReply{
		Term:        int64(r.currentTerm),
		VoteGranted: false,
	}

	if req.Term > int64(r.currentTerm) {
		r.state = raft_replicator.Follower
		r.currentTerm = uint64(req.Term)
		r.votedFor = ""
		_ = r.stableStore.SetState(r.currentTerm, "")
	}

	if req.Term < int64(r.currentTerm) {
		return reply, nil
	}

	lastIdx, lastTerm, _ := r.logStore.LastIndexAndTerm()
	logIsUpToDate := req.LastLogTerm > int64(lastTerm) || (req.LastLogTerm == int64(lastTerm) && req.LastLogIndex >= int64(lastIdx))

	if (r.votedFor == "" || r.votedFor == req.CandidateID) && logIsUpToDate {
		r.votedFor = req.CandidateID
		_ = r.stableStore.SetState(r.currentTerm, r.votedFor)
		r.electionResetEvent = time.Now()
		reply.VoteGranted = true
		reply.Term = int64(r.currentTerm)
	}

	return reply, nil
}

func (r *DurableRaftReplicator) checkCompaction() {
	lastIdx, _, _ := r.logStore.LastIndexAndTerm()
	if lastIdx > r.config.SnapshotThresholdLogs {
		// Compaction logic Flow 4
	}
}

func randomElectionTimeout() time.Duration {
	return time.Duration(150+rand.Intn(150)) * time.Millisecond
}
