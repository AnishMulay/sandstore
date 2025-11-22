package raft_replicator

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	pmr "github.com/AnishMulay/sandstore/internal/posix_metadata_replicator"
)

type RaftPosixMetadataReplicator struct {
	mu sync.Mutex

	// Dependencies
	id             string
	clusterService cluster_service.ClusterService
	comm           communication.Communicator
	ls             log_service.LogService
	applier        pmr.ApplyFunc

	// State
	state       RaftState
	currentTerm int64
	votedFor    string
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

func NewRaftPosixMetadataReplicator(
	id string,
	cs cluster_service.ClusterService,
	comm communication.Communicator,
	ls log_service.LogService,
) *RaftPosixMetadataReplicator {
	return &RaftPosixMetadataReplicator{
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

func (r *RaftPosixMetadataReplicator) Start(applier pmr.ApplyFunc) error {
	r.applier = applier
	r.ls.Info(log_service.LogEvent{Message: "Starting Raft Metadata Replicator"})

	go r.run()
	return nil
}

func (r *RaftPosixMetadataReplicator) Stop() error {
	r.ls.Info(log_service.LogEvent{Message: "Stopping Raft Metadata Replicator"})
	close(r.stopCh)
	return nil
}

func (r *RaftPosixMetadataReplicator) Replicate(ctx context.Context, data []byte) error {
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

func (r *RaftPosixMetadataReplicator) run() {
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

func (r *RaftPosixMetadataReplicator) startElection() {
	r.becomeCandidate()
	savedTerm := r.currentTerm
	savedLastLogIndex := r.getLastLogIndex()
	savedLastLogTerm := r.getLastLogTerm()

	r.ls.Info(log_service.LogEvent{Message: "Starting Election", Metadata: map[string]any{"term": savedTerm}})

	nodes, _ := r.clusterService.GetHealthyNodes()
	var peers []cluster_service.Node
	for _, n := range nodes {
		if n.ID != r.id {
			peers = append(peers, n)
		}
	}

	votesReceived := 1 // Vote for self
	
	for _, peer := range peers {
		go func(peer cluster_service.Node) {
			args := RequestVoteArgs{
				Term:         savedTerm,
				CandidateID:  r.id,
				LastLogIndex: savedLastLogIndex,
				LastLogTerm:  savedLastLogTerm,
			}

			// Send RPC
			msg := communication.Message{
				From:    r.comm.Address(),
				Type:    communication.MessageTypeRequestVote, // Ensure this is defined in communication package
				Payload: args,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			resp, err := r.comm.Send(ctx, peer.Address, msg)
			if err != nil {
				return // Fail silently
			}

			if resp.Code != communication.CodeOK {
				return
			}

			var reply RequestVoteReply
			if err := json.Unmarshal(resp.Body, &reply); err != nil {
				return
			}

			// Handle Reply
			r.mu.Lock()
			defer r.mu.Unlock()

			if r.state != Candidate || r.currentTerm != savedTerm {
				return
			}

			if reply.Term > r.currentTerm {
				r.becomeFollower(reply.Term)
				return
			}

			if reply.VoteGranted {
				votesReceived++
				quorum := (len(nodes) / 2) + 1
				if votesReceived >= quorum {
					r.ls.Info(log_service.LogEvent{Message: "Election Won", Metadata: map[string]any{"term": r.currentTerm}})
					r.becomeLeader()
				}
			}
		}(peer)
	}
}

func (r *RaftPosixMetadataReplicator) broadcastAppendEntries() {
	r.mu.Lock()
	if r.state != Leader {
		r.mu.Unlock()
		return
	}
	savedTerm := r.currentTerm
	savedCommitIndex := r.commitIndex
	leaderID := r.id
	
	nodes, _ := r.clusterService.GetHealthyNodes()
	
	type peerState struct {
		node         cluster_service.Node
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
			args := AppendEntriesArgs{
				Term:         savedTerm,
				LeaderID:     leaderID,
				PrevLogIndex: w.prevLogIndex,
				PrevLogTerm:  w.prevLogTerm,
				Entries:      w.entries,
				LeaderCommit: savedCommitIndex,
			}

			msg := communication.Message{
				From:    r.comm.Address(),
				Type:    communication.MessageTypeAppendEntries,
				Payload: args,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			resp, err := r.comm.Send(ctx, w.node.Address, msg)
			if err != nil {
				return
			}
			if resp.Code != communication.CodeOK {
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

// --- RPC Handlers (Called by Server wrapper) ---

// These are already fully implemented in core_logic.go (HandleRequestVote, HandleAppendEntries).
// The server wrapper (e.g., PosixServer) will need to:
// 1. Receive the message
// 2. Deserialize payload into RequestVoteArgs
// 3. Call replicator.HandleRequestVote(args)
// 4. Serialize the returned reply to JSON
// 5. Send back in communication.Response