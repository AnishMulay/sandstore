package cluster_service

import (
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"golang.org/x/exp/rand"
)

type RaftState int

const (
	Leader RaftState = iota
	Follower
	Candidate
)

type RaftClusterService struct {
	id            string
	state         RaftState
	currentTerm   int64
	votedFor      string
	leaderID      string
	electionTimer *time.Timer

	comm communication.Communicator
	ls   log_service.LogService
	mu   sync.Mutex
}

func (r *RaftClusterService) resetElectionTimer() {
	if r.electionTimer != nil {
		r.electionTimer.Stop()
	}

	timeout := time.Duration(rand.Intn(150)+150) * time.Millisecond
	r.electionTimer = time.AfterFunc(timeout, r.startElection)
}
