package raft_replicator

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
)

type RaftMetadataReplicator struct {
	mu sync.Mutex

	// Dependencies
	id             string
	clusterService cluster_service.NewClusterService
	comm           communication.Communicator
	ls             log_service.LogService
	applier        pmr.ApplyFunc

	// State
	state       RaftState
	currentTerm int64
	votedFor    string
	voteCount   int
	log         []LogEntry

	// Volatile State
	commitIndex int64
	lastApplied int64

	// Leader State
	nextIndex  map[string]int64
	matchIndex map[string]int64
	leaderID   string

	// Event Loop Control
	electionResetEvent time.Time
	stopCh             chan struct{}

	// Waiting Requests (Index -> Channel)
	pendingRequests map[int64]chan error
	pendingMu       sync.Mutex
}

func NewRaftMetadataReplicator(
	id string,
	cs cluster_service.NewClusterService,
	comm communication.Communicator,
	ls log_service.LogService,
) *RaftMetadataReplicator {
	return &RaftMetadataReplicator{
		id:                 id,
		clusterService:     cs,
		comm:               comm,
		ls:                 ls,
		state:              Follower,
		currentTerm:        0,
		votedFor:           "",
		log:                make([]LogEntry, 0),
		nextIndex:          make(map[string]int64),
		matchIndex:         make(map[string]int64),
		stopCh:             make(chan struct{}),
		pendingRequests:    make(map[int64]chan error),
		electionResetEvent: time.Now(),
	}
}

// --- Interface Implementation ---

func (r *RaftMetadataReplicator) Start(applier pmr.ApplyFunc) error {
	r.applier = applier
	r.ls.Info(log_service.LogEvent{Message: "Starting Raft Metadata Replicator"})

	go r.run()
	return nil
}

func (r *RaftMetadataReplicator) Stop() error {
	r.ls.Info(log_service.LogEvent{Message: "Stopping Raft Metadata Replicator"})
	close(r.stopCh)
	return nil
}

func (r *RaftMetadataReplicator) Replicate(ctx context.Context, data []byte) error {
	r.mu.Lock()
	if r.state != Leader {
		leaderID := r.leaderID
		r.mu.Unlock()

		// Try to find leader address
		nodes, _ := r.clusterService.GetHealthyNodes()
		leaderAddr := ""
		for _, n := range nodes {
			if n.ID == leaderID {
				leaderAddr = n.Address
				break
			}
		}

		return ErrNotLeader{LeaderID: leaderID, LeaderAddr: leaderAddr}
	}

	// Append to local log
	entry := LogEntry{
		Index:     r.getLastLogIndex() + 1,
		Term:      r.currentTerm,
		Data:      data,
		Timestamp: time.Now(),
	}
	r.log = append(r.log, entry)
	r.ls.Info(log_service.LogEvent{
		Message:  "Leader appended new entry",
		Metadata: map[string]any{"index": entry.Index, "term": entry.Term},
	})

	// Setup channel to wait for commit
	resultCh := make(chan error, 1)
	r.pendingMu.Lock()
	r.pendingRequests[entry.Index] = resultCh
	r.pendingMu.Unlock()

	r.mu.Unlock()

	go r.broadcastAppendEntries()

	// Wait for Commit or Timeout
	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		r.pendingMu.Lock()
		delete(r.pendingRequests, entry.Index)
		r.pendingMu.Unlock()
		return ctx.Err()
	case <-r.stopCh:
		return nil
	}
}

// --- Event Loop ---

func (r *RaftMetadataReplicator) run() {
	electionTimeout := getRandomElectionTimeout()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.mu.Lock()
			elapsed := time.Since(r.electionResetEvent)

			// Election Logic
			if r.state != Leader && elapsed >= electionTimeout {
				r.startElection()
				electionTimeout = getRandomElectionTimeout()
				r.electionResetEvent = time.Now()
			}

			// Heartbeat Logic
			if r.state == Leader && elapsed >= 50*time.Millisecond {
				r.mu.Unlock()
				r.broadcastAppendEntries()
				r.mu.Lock()

				r.electionResetEvent = time.Now()
			}
			r.mu.Unlock()
		}
	}
}

func (r *RaftMetadataReplicator) startElection() {
	r.becomeCandidate()
	savedTerm := r.currentTerm
	savedLastLogIndex := r.getLastLogIndex()
	savedLastLogTerm := r.getLastLogTerm()

	r.ls.Info(log_service.LogEvent{Message: "Starting Election", Metadata: map[string]any{"term": savedTerm}})

	nodes, _ := r.clusterService.GetAllNodes()

	// Quorum is based on total known nodes
	// Ideally strictly based on config, but here based on discovery
	totalNodes := len(nodes)
	quorum := (totalNodes / 2) + 1

	// If single node cluster, win immediately
	if totalNodes == 1 {
		r.becomeLeader()
		return
	}

	var peers []cluster_service.SafeNode
	for _, n := range nodes {
		if n.ID != r.id {
			peers = append(peers, n)
		}
	}

	// State for the election
	var votesReceived int32 = 1 // Vote for self
	var voteMu sync.Mutex

	for _, peer := range peers {
		go func(peer cluster_service.SafeNode) {
			args := RequestVoteArgs{
				Term:         savedTerm,
				CandidateID:  r.id,
				LastLogIndex: savedLastLogIndex,
				LastLogTerm:  savedLastLogTerm,
			}

			msg := communication.Message{
				From:    r.comm.Address(),
				Type:    "raft_request_vote", // Use string literal to avoid circular dependency
				Payload: args,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			resp, err := r.comm.Send(ctx, peer.Address, msg)
			if err != nil {
				r.ls.Debug(log_service.LogEvent{
					Message:  "Vote Request Failed",
					Metadata: map[string]any{"peer": peer.Address, "error": err.Error()},
				})
				return
			}

			// Verify response code
			if resp.Code != communication.CodeOK {
				return
			}

			var reply RequestVoteReply
			if err := json.Unmarshal(resp.Body, &reply); err != nil {
				return
			}

			r.mu.Lock()
			defer r.mu.Unlock()

			// Guard: If state changed, ignore
			if r.state != Candidate || r.currentTerm != savedTerm {
				return
			}

			if reply.Term > r.currentTerm {
				r.becomeFollower(reply.Term)
				return
			}

			if reply.VoteGranted {
				voteMu.Lock()
				votesReceived++
				currentVotes := votesReceived
				voteMu.Unlock()

				r.ls.Debug(log_service.LogEvent{
					Message:  "Vote Granted",
					Metadata: map[string]any{"from": peer.Address, "votes": currentVotes, "quorum": quorum},
				})

				// The provided snippet for "Checking Commit Index" and `matchCount` seems to belong to `advanceCommitIndex`.
				// Assuming the user intended to add this log within `advanceCommitIndex` or a similar commit-related logic.
				// Since `advanceCommitIndex` is called from `broadcastAppendEntries`, I'll place the log there.
				// However, the snippet provided is directly in the `startElection` context.
				// Given the instruction "Add debug logs in broadcastAppendEntries and advanceCommitIndex",
				// and the content of the log, I will place it in `advanceCommitIndex` as that's where `matchCount` and `n` would be relevant.
				// The snippet provided in the prompt is syntactically incorrect and contextually misplaced for `startElection`.
				// I will apply the log to `advanceCommitIndex` as per the instruction's intent.

				if int(currentVotes) >= quorum {
					// Check again to be sure we didn't already become leader in another goroutine
					if r.state == Candidate {
						r.ls.Info(log_service.LogEvent{Message: "Election Won", Metadata: map[string]any{"term": r.currentTerm}})
						r.becomeLeader()
						// Force heartbeat immediately handled by becomeLeader
					}
				}
			}
		}(peer)
	}
}

func (r *RaftMetadataReplicator) broadcastAppendEntries() {
	r.mu.Lock()
	if r.state != Leader {
		r.mu.Unlock()
		return
	}
	savedTerm := r.currentTerm
	savedCommitIndex := r.commitIndex
	leaderID := r.id

	nodes, _ := r.clusterService.GetAllNodes()

	r.ls.Debug(log_service.LogEvent{
		Message:  "Broadcasting AppendEntries",
		Metadata: map[string]any{"peers": len(nodes), "term": r.currentTerm},
	})

	type peerState struct {
		node         cluster_service.SafeNode
		nextIndex    int64
		prevLogIndex int64
		prevLogTerm  int64
		entries      []LogEntry
	}

	var workList []peerState

	for _, node := range nodes {
		if node.ID == r.id {
			continue
		}

		nextIdx := r.nextIndex[node.ID]
		// If nextIdx is 0 (uninitialized), fix it
		if nextIdx == 0 {
			nextIdx = 1
		}

		prevLogIndex := nextIdx - 1
		prevLogTerm := r.getLogTerm(prevLogIndex)

		var entries []LogEntry
		lastLogIdx := r.getLastLogIndex()

		if nextIdx <= lastLogIdx {
			start := nextIdx - 1
			if start < int64(len(r.log)) {
				src := r.log[start:]
				entries = make([]LogEntry, len(src))
				copy(entries, src)
			}
		}

		workList = append(workList, peerState{
			node:         node,
			nextIndex:    nextIdx,
			prevLogIndex: prevLogIndex,
			prevLogTerm:  prevLogTerm,
			entries:      entries,
		})
	}
	r.mu.Unlock()

	for _, work := range workList {
		go func(w peerState) {
			// Serialize entries to match communication.AppendEntriesRequest
			entriesBytes, err := json.Marshal(w.entries)
			if err != nil {
				r.ls.Error(log_service.LogEvent{
					Message:  "Failed to marshal entries",
					Metadata: map[string]any{"error": err.Error()},
				})
				return
			}

			args := communication.AppendEntriesRequest{
				Term:         savedTerm,
				LeaderID:     leaderID,
				PrevLogIndex: w.prevLogIndex,
				PrevLogTerm:  w.prevLogTerm,
				Entries:      entriesBytes,
				LeaderCommit: savedCommitIndex,
			}

			msg := communication.Message{
				From:    r.comm.Address(),
				Type:    "raft_append_entries", // Use string literal to match server expectation
				Payload: args,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			resp, err := r.comm.Send(ctx, w.node.Address, msg)
			if err != nil {
				r.ls.Error(log_service.LogEvent{
					Message:  "AppendEntries Failed",
					Metadata: map[string]any{"to": w.node.Address, "error": err.Error()},
				})
				return
			}
			if resp.Code != communication.CodeOK {
				r.ls.Debug(log_service.LogEvent{
					Message:  "AppendEntries Response Not OK",
					Metadata: map[string]any{"to": w.node.Address, "code": resp.Code, "body": string(resp.Body)},
				})
				return
			}

			var reply AppendEntriesReply
			if err := json.Unmarshal(resp.Body, &reply); err != nil {
				return
			}

			r.mu.Lock()
			defer r.mu.Unlock()

			if r.state != Leader || r.currentTerm != savedTerm {
				return
			}

			if reply.Term > r.currentTerm {
				r.becomeFollower(reply.Term)
				return
			}

			if reply.Success {
				newMatchIndex := w.prevLogIndex + int64(len(w.entries))
				if newMatchIndex > r.matchIndex[w.node.ID] {
					r.matchIndex[w.node.ID] = newMatchIndex
					r.nextIndex[w.node.ID] = newMatchIndex + 1
					r.advanceCommitIndex()
				}
			} else {
				r.ls.Debug(log_service.LogEvent{
					Message:  "AppendEntries Rejected",
					Metadata: map[string]any{"to": w.node.Address, "term": reply.Term, "conflict": reply.ConflictIndex},
				})
				newNext := r.nextIndex[w.node.ID] - 1
				if newNext < 1 {
					newNext = 1
				}
				r.nextIndex[w.node.ID] = newNext

				if reply.ConflictIndex > 0 {
					r.nextIndex[w.node.ID] = reply.ConflictIndex
				}
			}
		}(work)
	}
}

// --- RPC Handlers ---

func (r *RaftMetadataReplicator) HandleRequestVote(args RequestVoteArgs) (*RequestVoteReply, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reply := &RequestVoteReply{
		Term:        r.currentTerm,
		VoteGranted: false,
	}

	// 1. Term Check
	if args.Term > r.currentTerm {
		r.becomeFollower(args.Term)
	}

	if args.Term < r.currentTerm {
		return reply, nil
	}

	// 2. Vote Check
	// If we haven't voted, or we voted for this candidate
	if (r.votedFor == "" || r.votedFor == args.CandidateID) &&
		r.isLogUpToDate(args.LastLogIndex, args.LastLogTerm) {

		r.votedFor = args.CandidateID
		r.electionResetEvent = time.Now()
		reply.VoteGranted = true
		reply.Term = r.currentTerm

		r.ls.Info(log_service.LogEvent{
			Message:  "Vote Granted",
			Metadata: map[string]any{"candidate": args.CandidateID, "term": args.Term},
		})
	}

	return reply, nil
}

func (r *RaftMetadataReplicator) HandleAppendEntries(req communication.AppendEntriesRequest) (*AppendEntriesReply, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reply := &AppendEntriesReply{
		Term:    r.currentTerm,
		Success: false,
	}

	// Deserialize entries
	var entries []LogEntry
	if len(req.Entries) > 0 {
		if err := json.Unmarshal(req.Entries, &entries); err != nil {
			r.ls.Error(log_service.LogEvent{
				Message:  "Failed to unmarshal entries",
				Metadata: map[string]any{"error": err.Error()},
			})
			return nil, err
		}
	}

	// 1. Term Check
	if req.Term > r.currentTerm {
		r.becomeFollower(req.Term)
	}

	if req.Term < r.currentTerm {
		return reply, nil
	}

	// We recognize the leader
	r.electionResetEvent = time.Now()
	r.leaderID = req.LeaderID
	if r.state != Follower {
		r.becomeFollower(req.Term)
	}

	// 2. Log Consistency Check
	// Does our log contain an entry at PrevLogIndex with PrevLogTerm?
	if req.PrevLogIndex > 0 {
		lastIndex := r.getLastLogIndex()
		if req.PrevLogIndex > lastIndex {
			reply.ConflictIndex = lastIndex + 1
			return reply, nil
		}

		term := r.getLogTerm(req.PrevLogIndex)
		if term != req.PrevLogTerm {
			reply.ConflictTerm = term
			// Optimization: Find first index of ConflictTerm
			// For now, just fail
			return reply, nil
		}
	}

	// 3. Append New Entries
	for i, entry := range entries {
		index := entry.Index
		if index > r.getLastLogIndex() {
			r.log = append(r.log, entries[i:]...)
			break
		}

		// If existing entry conflicts (same index, different term), delete existing and all that follow
		existingTerm := r.getLogTerm(index)
		if existingTerm != entry.Term {
			// Delete from here onwards
			// Adjust slice index (1-based log index vs 0-based slice)
			sliceIdx := index - 1
			r.log = r.log[:sliceIdx]
			r.log = append(r.log, entries[i:]...)
			break
		}
	}

	// 4. Update Commit Index
	if req.LeaderCommit > r.commitIndex {
		lastNewIndex := req.PrevLogIndex + int64(len(entries))
		if req.LeaderCommit < lastNewIndex {
			r.commitIndex = req.LeaderCommit
		} else {
			r.commitIndex = lastNewIndex
		}
		r.applyLogs()
	}

	reply.Success = true
	return reply, nil
}
