package cluster_service

import (
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
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
