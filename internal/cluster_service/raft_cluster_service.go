package cluster_service

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"golang.org/x/exp/rand"
)

// MetadataLogInterface defines the interface for accessing metadata log
type MetadataLogInterface interface {
	GetLastLogIndex() int64
	GetLastLogTerm() int64
	GetEntryAtIndex(index int64) interface{} // Returns entry with Index and Term fields
}

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

	// Raft log state tracking
	nextIndex  map[string]int64 // next log index to send to each peer
	matchIndex map[string]int64 // highest log index replicated on each peer
	commitIndex int64           // highest log entry known to be committed
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
		nextIndex:   make(map[string]int64),
		matchIndex:  make(map[string]int64),
		commitIndex: 0,
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
	r.ls.Debug(log_service.LogEvent{
		Message:  "Resetting election timer",
		Metadata: map[string]any{"timeout": timeout},
	})
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

	// Start election timeout to reset if no leader elected
	go r.electionTimeout(term)

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
	r.ls.Debug(log_service.LogEvent{
		Message:  "Registered vote",
		Metadata: map[string]any{"voteCount": r.voteCount},
	})
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

	if len(req.Entries) > 0 {
		r.ls.Info(log_service.LogEvent{
			Message:  "Received append entries with data",
			Metadata: map[string]any{
				"term": req.Term, 
				"leaderID": req.LeaderID, 
				"entriesSize": len(req.Entries),
				"entries": string(req.Entries),
			},
		})
	}

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
			r.sendAppendEntries(n.Address, []byte{}, nil) // Empty for heartbeat, no log needed
		}(node)
	}
}

func (r *RaftClusterService) sendAppendEntries(nodeAddress string, entriesData []byte, metadataLog MetadataLogInterface) bool {
	var prevLogIndex, prevLogTerm int64

	if metadataLog != nil {
		peerNextIndex := r.nextIndex[nodeAddress]
		if peerNextIndex == 0 {
			peerNextIndex = metadataLog.GetLastLogIndex() + 1
		}
		prevLogIndex = peerNextIndex - 1
		
		if prevLogIndex > 0 {
			if prevEntryInterface := metadataLog.GetEntryAtIndex(prevLogIndex); prevEntryInterface != nil {
				prevLogTerm = getTermFromEntry(prevEntryInterface)
			}
		}
	}

	req := communication.AppendEntriesRequest{
		Term:         r.currentTerm,
		LeaderID:     r.id,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entriesData,
		LeaderCommit: r.commitIndex,
	}

	msg := communication.Message{
		From:    r.comm.Address(),
		Type:    communication.MessageTypeAppendEntries,
		Payload: req,
	}

	resp, err := r.comm.Send(context.Background(), nodeAddress, msg)
	
	// r.ls.Info(log_service.LogEvent{
	// 	Message:  "Response from append entries",
	// 	Metadata: map[string]any{"nodeAddress": nodeAddress, "error": err, "response": resp},
	// })

	return err == nil && resp.Code == communication.CodeOK
}

// getTermFromEntry extracts Term field from any log entry using reflection
func getTermFromEntry(entry interface{}) int64 {
	if entry == nil {
		return 0
	}
	v := reflect.ValueOf(entry)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		termField := v.FieldByName("Term")
		if termField.IsValid() && termField.CanInterface() {
			if term, ok := termField.Interface().(int64); ok {
				return term
			}
		}
	}
	return 0
}

func (r *RaftClusterService) IsLeader() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.state == Leader
}

func (r *RaftClusterService) electionTimeout(term int64) {
	time.Sleep(500 * time.Millisecond) // Wait for election to complete
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.currentTerm == term && r.state == Candidate {
		r.ls.Debug(log_service.LogEvent{
			Message:  "Election timeout - restarting election",
			Metadata: map[string]any{"term": term},
		})
		r.resetElectionTimer()
	}
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

func (r *RaftClusterService) GetCurrentTerm() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.currentTerm
}

func (r *RaftClusterService) ReplicateEntries(entriesData []byte, logIndex int64, metadataLog MetadataLogInterface, callback func(int64, bool)) {	
	r.ls.Info(log_service.LogEvent{
		Message: "Starting entry replication",
		Metadata: map[string]any{
			"logIndex": logIndex,
			"entriesSize": len(entriesData),
			"currentTerm": r.currentTerm,
		},
	})

	nodes, err := r.GetHealthyNodes()
	if err != nil {
		r.ls.Info(log_service.LogEvent{
			Message: "Replication failed - no healthy nodes",
			Metadata: map[string]any{"error": err.Error(), "logIndex": logIndex},
		})
		callback(logIndex, false)
		return
	}

	r.ls.Info(log_service.LogEvent{
		Message: "Replication cluster info",
		Metadata: map[string]any{
			"totalNodes": len(nodes),
			"quorumSize": (len(nodes) / 2) + 1,
			"logIndex": logIndex,
		},
	})

	for _, node := range nodes {
		if node.ID == r.id {
			continue
		}
		if _, exists := r.nextIndex[node.ID]; !exists {
			r.nextIndex[node.ID] = metadataLog.GetLastLogIndex() + 1
			r.matchIndex[node.ID] = 0
		}
	}

	ackCount := 1 // Leader counts
	quorumSize := (len(nodes) / 2) + 1

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, node := range nodes {
		if node.ID == r.id {
			continue
		}

		wg.Add(1)
		go func(n Node) {
			defer wg.Done()
			
			r.ls.Info(log_service.LogEvent{
				Message: "Sending append entries to node",
				Metadata: map[string]any{
					"nodeID": n.ID,
					"nodeAddress": n.Address,
					"logIndex": logIndex,
				},
			})

			if r.sendAppendEntries(n.Address, entriesData, metadataLog) {
				mu.Lock()
				r.matchIndex[n.ID] = logIndex
				r.nextIndex[n.ID] = logIndex + 1
				ackCount++
				
				r.ls.Info(log_service.LogEvent{
					Message: "Received acknowledgment from node",
					Metadata: map[string]any{
						"nodeID": n.ID,
						"ackCount": ackCount,
						"quorumSize": quorumSize,
						"logIndex": logIndex,
					},
				})
				mu.Unlock()
			} else {
				r.ls.Info(log_service.LogEvent{
					Message: "Failed to get acknowledgment from node",
					Metadata: map[string]any{
						"nodeID": n.ID,
						"nodeAddress": n.Address,
						"logIndex": logIndex,
					},
				})
			}
		}(node)
	}

	wg.Wait()

	r.ls.Info(log_service.LogEvent{
		Message: "Replication completed",
		Metadata: map[string]any{
			"ackCount": ackCount,
			"quorumSize": quorumSize,
			"quorumReached": ackCount >= quorumSize,
			"logIndex": logIndex,
			"commitIndex": r.commitIndex,
		},
	})

	// Update commit index if we have quorum
	if ackCount >= quorumSize {
		r.commitIndex = logIndex
	}

	// Call callback immediately while we still have the context
	callback(logIndex, ackCount >= quorumSize)
}


