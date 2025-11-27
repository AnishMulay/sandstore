package raft_replicator

import (
	"math/rand"
	"time"

	"github.com/AnishMulay/sandstore/internal/log_service"
)

// becomeFollower transitions the node to follower state
func (r *RaftMetadataReplicator) becomeFollower(term int64) {
	r.state = Follower
	r.currentTerm = term
	r.votedFor = ""
	r.electionResetEvent = time.Now()

	r.ls.Info(log_service.LogEvent{
		Message:  "Became Follower",
		Metadata: map[string]any{"term": term},
	})
}

// becomeCandidate transitions the node to candidate state
func (r *RaftMetadataReplicator) becomeCandidate() {
	r.state = Candidate
	r.currentTerm++
	r.votedFor = r.id
	r.voteCount = 1 // Vote for self
	r.electionResetEvent = time.Now()

	r.ls.Info(log_service.LogEvent{
		Message:  "Became Candidate",
		Metadata: map[string]any{"term": r.currentTerm},
	})
}

// becomeLeader transitions the node to leader state
func (r *RaftMetadataReplicator) becomeLeader() {
	r.state = Leader
	r.leaderID = r.id

	// Reinitialize volatile leader state
	// nextIndex: index of the next log entry to send to that server (initialized to leader last log index + 1)
	// matchIndex: index of highest log entry known to be replicated on server (initialized to 0)
	nodes, _ := r.clusterService.GetHealthyNodes()
	for _, node := range nodes {
		r.nextIndex[node.ID] = r.getLastLogIndex() + 1
		r.matchIndex[node.ID] = 0
	}

	r.ls.Info(log_service.LogEvent{
		Message:  "Became Leader",
		Metadata: map[string]any{"term": r.currentTerm},
	})

	// Send initial heartbeat immediately
	go r.broadcastAppendEntries()
}

// getLastLogIndex returns the index of the last entry in the log
func (r *RaftMetadataReplicator) getLastLogIndex() int64 {
	if len(r.log) > 0 {
		return r.log[len(r.log)-1].Index
	}
	return 0
}

// getLastLogTerm returns the term of the last entry in the log
func (r *RaftMetadataReplicator) getLastLogTerm() int64 {
	if len(r.log) > 0 {
		return r.log[len(r.log)-1].Term
	}
	return 0
}

// getLogTerm returns the term of the entry at the specific index
func (r *RaftMetadataReplicator) getLogTerm(index int64) int64 {
	if index == 0 {
		return 0
	}
	// Since our log is 1-indexed but stored in 0-indexed slice
	// and we don't have a dummy entry at 0 in this implementation logic:
	// We need to adjust carefully.
	// Let's assume log storage: [Index 1, Index 2, Index 3]
	// If requested index > len, return 0
	if index > int64(len(r.log)) {
		return 0
	}
	return r.log[index-1].Term
}

// isLogUpToDate checks if the candidate's log is at least as up-to-date as ours
func (r *RaftMetadataReplicator) isLogUpToDate(cLastIndex, cLastTerm int64) bool {
	myLastTerm := r.getLastLogTerm()
	myLastIndex := r.getLastLogIndex()

	if cLastTerm != myLastTerm {
		return cLastTerm > myLastTerm
	}
	return cLastIndex >= myLastIndex
}

// advanceCommitIndex checks if the matchIndex values allow us to increment commitIndex
func (r *RaftMetadataReplicator) advanceCommitIndex() {
	// If there exists an N such that N > commitIndex, a majority of matchIndex[i] ≥ N,
	// and log[N].term == currentTerm: set commitIndex = N

	if r.state != Leader {
		return
	}

	start := r.commitIndex + 1
	end := r.getLastLogIndex()

	// We iterate backwards to find the highest N
	for n := end; n >= start; n-- {
		term := r.getLogTerm(n)
		if term != r.currentTerm {
			continue
		}

		matchCount := 1 // Count self
		nodes, _ := r.clusterService.GetHealthyNodes()
		quorum := (len(nodes) / 2) + 1

		for _, node := range nodes {
			if node.ID == r.id {
				continue
			}
			if r.matchIndex[node.ID] >= n {
				matchCount++
			}
		}

		r.ls.Debug(log_service.LogEvent{
			Message:  "Checking Commit Index",
			Metadata: map[string]any{"n": n, "matchCount": matchCount, "quorum": quorum},
		})

		if matchCount >= quorum {
			r.commitIndex = n
			r.ls.Info(log_service.LogEvent{
				Message:  "Commit Index Advanced",
				Metadata: map[string]any{"newCommitIndex": n},
			})
			r.applyLogs()
			break
		}
	}
}

// applyLogs sends committed entries to the service application callback
func (r *RaftMetadataReplicator) applyLogs() {
	for r.lastApplied < r.commitIndex {
		r.lastApplied++
		entry := r.log[r.lastApplied-1] // Adjust for slice index

		r.ls.Debug(log_service.LogEvent{
			Message:  "Applying Entry",
			Metadata: map[string]any{"index": entry.Index},
		})

		// 1. Execute callback to Service
		if r.applier != nil {
			err := r.applier(entry.Data)
			if err != nil {
				r.ls.Error(log_service.LogEvent{
					Message:  "Service failed to apply log entry",
					Metadata: map[string]any{"index": entry.Index, "error": err.Error()},
				})
				// In a real system, we might panic here because state machine divergence is fatal
			}
		}

		// 2. Notify any waiting Replicate() calls
		r.notifyPendingRequest(entry.Index, nil)
	}
}

func (r *RaftMetadataReplicator) notifyPendingRequest(index int64, err error) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()

	if ch, ok := r.pendingRequests[index]; ok {
		select {
		case ch <- err:
		default:
		}
		close(ch)
		delete(r.pendingRequests, index)
	}
}

// getRandomElectionTimeout returns a duration.
// UPDATED: Increased range to 300-600ms to reduce split vote probability in local envs.
func getRandomElectionTimeout() time.Duration {
	return time.Duration(3000+rand.Intn(3000)) * time.Millisecond
}
