package cluster_service

import (
	"context"
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
	nodes         []Node
	id            string
	state         RaftState
	currentTerm   int64
	votedFor      string
	voteCount     int64
	leaderID      string
	electionTimer *time.Timer

	comm communication.Communicator
	ls   log_service.LogService
	mu   sync.Mutex
}

func NewRaftClusterService(id string, nodes []Node, comm communication.Communicator, ls log_service.LogService) *RaftClusterService {
	return &RaftClusterService{
		nodes:       nodes,
		id:          id,
		state:       Follower,
		currentTerm: 0,
		votedFor:    "",
		voteCount:   0,
		leaderID:    "",
		comm:        comm,
		ls:          ls,
	}
}

func (r *RaftClusterService) Start() {
	r.ls.Info(log_service.LogEvent{
		Message:  "Starting Raft cluster service",
		Metadata: map[string]any{"nodeID": r.id},
	})
	r.resetElectionTimer()
}

func (r *RaftClusterService) RegisterNode(node Node) error {
	r.ls.Info(log_service.LogEvent{
		Message:  "Registering node",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address, "healthy": node.Healthy},
	})

	if node.ID == "" {
		r.ls.Error(log_service.LogEvent{
			Message:  "Invalid node ID",
			Metadata: map[string]any{"nodeID": node.ID},
		})
		return ErrInvalidNodeID
	}

	if node.Address == "" {
		r.ls.Error(log_service.LogEvent{
			Message:  "Invalid node address",
			Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
		})
		return ErrInvalidNodeAddress
	}

	for _, n := range r.nodes {
		if n.ID == node.ID {
			r.ls.Error(log_service.LogEvent{
				Message:  "Node already exists",
				Metadata: map[string]any{"nodeID": node.ID},
			})
			return ErrNodeAlreadyExists
		}
	}

	r.nodes = append(r.nodes, node)

	r.ls.Info(log_service.LogEvent{
		Message:  "Node registered successfully",
		Metadata: map[string]any{"nodeID": node.ID, "totalNodes": len(r.nodes)},
	})

	return nil
}

func (r *RaftClusterService) DeregisterNode(node Node) error {
	r.ls.Info(log_service.LogEvent{
		Message:  "Deregistering node",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
	})

	for i, n := range r.nodes {
		if n.ID == node.ID {
			r.nodes = append(r.nodes[:i], r.nodes[i+1:]...)
			r.ls.Info(log_service.LogEvent{
				Message:  "Node deregistered successfully",
				Metadata: map[string]any{"nodeID": node.ID, "totalNodes": len(r.nodes)},
			})
			return nil
		}
	}

	r.ls.Error(log_service.LogEvent{
		Message:  "Node not found for deregistration",
		Metadata: map[string]any{"nodeID": node.ID, "address": node.Address},
	})

	return ErrNodeNotFound
}

func (r *RaftClusterService) GetHealthyNodes() ([]Node, error) {
	r.ls.Debug(log_service.LogEvent{
		Message:  "Getting healthy nodes",
		Metadata: map[string]any{"totalNodes": len(r.nodes)},
	})

	var healthyNodes []Node
	for _, node := range r.nodes {
		if node.Healthy {
			healthyNodes = append(healthyNodes, node)
		}
	}

	if len(healthyNodes) == 0 {
		r.ls.Warn(log_service.LogEvent{
			Message:  "No healthy nodes available",
			Metadata: map[string]any{"totalNodes": len(r.nodes)},
		})
		return nil, ErrNoHealthyNodes
	}

	r.ls.Debug(log_service.LogEvent{
		Message:  "Healthy nodes retrieved",
		Metadata: map[string]any{"healthyNodes": len(healthyNodes), "totalNodes": len(r.nodes)},
	})

	return healthyNodes, nil
}

func (r *RaftClusterService) resetElectionTimer() {
	if r.electionTimer != nil {
		r.electionTimer.Stop()
	}

	timeout := time.Duration(rand.Intn(150)+150) * time.Millisecond
	r.electionTimer = time.AfterFunc(timeout, r.startElection)
}

func (r *RaftClusterService) startElection() {
	r.mu.Lock()
	r.state = Candidate
	r.currentTerm++
	r.votedFor = r.id
	r.voteCount = 1
	term := r.currentTerm
	r.mu.Unlock()

	r.ls.Info(log_service.LogEvent{
		Message:  "Starting election",
		Metadata: map[string]any{"term": term, "candidateID": r.id},
	})

	nodes, err := r.GetHealthyNodes()
	if err != nil {
		r.ls.Error(log_service.LogEvent{
			Message:  "Failed to get healthy nodes",
			Metadata: map[string]any{"error": err.Error()},
		})
		return
	}

	for _, node := range nodes {
		if node.ID == r.id {
			continue
		}

		go func(n Node) {
			ok := r.sendRequestVote(n.Address, term)
			if ok {
				r.registerVote()
			}
		}(node)
	}
}

func (r *RaftClusterService) sendRequestVote(nodeAddress string, term int64) bool {
	r.ls.Debug(log_service.LogEvent{
		Message:  "Sending request vote",
		Metadata: map[string]any{"nodeAddress": nodeAddress, "term": term},
	})

	req := communication.RequestVoteRequest{
		Term:         term,
		CandidateID:  r.id,
		LastLogIndex: 0, // TODO: Get from log service
		LastLogTerm:  0, // TODO: Get from log service
	}

	msg := communication.Message{
		From:    r.comm.Address(),
		Type:    communication.MessageTypeRequestVote,
		Payload: req,
	}

	resp, err := r.comm.Send(context.Background(), nodeAddress, msg)
	if err != nil {
		r.ls.Error(log_service.LogEvent{
			Message:  "Failed to send request vote",
			Metadata: map[string]any{"nodeAddress": nodeAddress, "error": err.Error()},
		})
		return false
	}

	voteGranted := resp.Code == communication.CodeOK

	r.ls.Debug(log_service.LogEvent{
		Message:  "Received request vote response",
		Metadata: map[string]any{"nodeAddress": nodeAddress, "voteGranted": voteGranted},
	})

	return voteGranted
}

func (r *RaftClusterService) registerVote() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.voteCount++
	nodes, _ := r.GetHealthyNodes()
	if r.state == Candidate && r.voteCount > int64(len(nodes))/2 {
		r.becomeLeader()
		r.ls.Info(log_service.LogEvent{
			Message:  "Became leader",
			Metadata: map[string]any{"term": r.currentTerm, "leaderID": r.leaderID},
		})
	}
}

func (r *RaftClusterService) becomeLeader() {
	r.state = Leader
	r.leaderID = r.id

	if r.electionTimer != nil {
		r.electionTimer.Stop()
	}

	r.startHeartbeats()
}

func (r *RaftClusterService) HandleRequestVote(req communication.RequestVoteRequest) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.Term < r.currentTerm {
		return false, nil
	}

	if req.Term > r.currentTerm {
		r.state = Follower
		r.currentTerm = req.Term
		r.votedFor = ""
		r.voteCount = 0
	}

	if r.votedFor == "" || r.votedFor == req.CandidateID {
		r.votedFor = req.CandidateID
		return true, nil
	}

	return false, nil
}

func (r *RaftClusterService) HandleAppendEntries(req communication.AppendEntriesRequest) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ls.Debug(log_service.LogEvent{
		Message:  "Handling append entries",
		Metadata: map[string]any{"term": req.Term, "leaderID": req.LeaderID, "currentTerm": r.currentTerm},
	})

	if req.Term < r.currentTerm {
		r.ls.Debug(log_service.LogEvent{
			Message:  "Rejecting append entries - stale term",
			Metadata: map[string]any{"requestTerm": req.Term, "currentTerm": r.currentTerm},
		})
		return false, nil
	}

	if req.Term > r.currentTerm {
		r.ls.Info(log_service.LogEvent{
			Message:  "Received higher term, converting to follower",
			Metadata: map[string]any{"oldTerm": r.currentTerm, "newTerm": req.Term},
		})
		r.currentTerm = req.Term
		r.votedFor = ""
		r.voteCount = 0
		r.state = Follower
	}

	if r.state == Follower || r.state == Candidate {
		r.state = Follower
		r.leaderID = req.LeaderID
		r.resetElectionTimer()

		r.ls.Debug(log_service.LogEvent{
			Message:  "Heartbeat received, election timer reset",
			Metadata: map[string]any{"leaderID": req.LeaderID, "term": req.Term},
		})
	}

	return true, nil
}

func (r *RaftClusterService) startHeartbeats() {
	r.ls.Info(log_service.LogEvent{
		Message:  "Starting heartbeats",
		Metadata: map[string]any{"term": r.currentTerm, "leaderID": r.leaderID},
	})

	heartBeatInterval := 100 * time.Millisecond
	heartbeatTicker := time.NewTicker(heartBeatInterval)

	go func() {
		defer heartbeatTicker.Stop()

		for {
			select {
			case <-heartbeatTicker.C:
				r.mu.Lock()

				if r.state != Leader {
					r.mu.Unlock()
					return
				}

				currentTerm := r.currentTerm
				r.mu.Unlock()

				r.sendHeartbeats(currentTerm)
			}
		}
	}()
}

func (r *RaftClusterService) sendHeartbeats(term int64) {
	nodes, err := r.GetHealthyNodes()
	if err != nil {
		r.ls.Error(log_service.LogEvent{
			Message:  "Failed to get healthy nodes",
			Metadata: map[string]any{"error": err.Error()},
		})
		return
	}

	for _, node := range nodes {
		if node.ID == r.id {
			continue
		}

		go func(n Node) {
			r.sendAppendEntries(n.Address, term)
		}(node)
	}
}

func (r *RaftClusterService) sendAppendEntries(nodeAddress string, term int64) {
	r.ls.Debug(log_service.LogEvent{
		Message:  "Sending append entries",
		Metadata: map[string]any{"nodeAddress": nodeAddress, "term": term},
	})

	req := communication.AppendEntriesRequest{
		Term:         term,
		LeaderID:     r.id,
		PrevLogIndex: 0, // TODO: Get from log service
		PrevLogTerm:  0, // TODO: Get from log service
	}

	msg := communication.Message{
		From:    r.comm.Address(),
		Type:    communication.MessageTypeAppendEntries,
		Payload: req,
	}

	resp, err := r.comm.Send(context.Background(), nodeAddress, msg)
	if err != nil {
		r.ls.Error(log_service.LogEvent{
			Message:  "Failed to send append entries",
			Metadata: map[string]any{"nodeAddress": nodeAddress, "error": err.Error()},
		})
		return
	}

	// might need to change this later to differentiate between kinds of failures
	if resp.Code != communication.CodeOK {
		r.ls.Error(log_service.LogEvent{
			Message:  "Failed to send append entries",
			Metadata: map[string]any{"nodeAddress": nodeAddress, "error": err.Error()},
		})
		return
	}

	r.ls.Debug(log_service.LogEvent{
		Message:  "Received append entries response",
		Metadata: map[string]any{"nodeAddress": nodeAddress},
	})
}

func (r *RaftClusterService) IsLeader() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.state == Leader
}

func (r *RaftClusterService) GetLeaderAddress() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state == Leader {
		return r.comm.Address()
	}

	for _, node := range r.nodes {
		if node.ID == r.leaderID {
			return node.Address
		}
	}

	return "" // No known leader
}
