package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

type ConvergedRouter struct {
	seeds []string
	comm  communication.Communicator

	mu         sync.RWMutex
	leaderAddr string
	allNodes   []string
	lastUpdate time.Time

	refreshMu sync.Mutex
}

func NewConvergedRouter(seeds []string, comm communication.Communicator) *ConvergedRouter {
	return &ConvergedRouter{
		seeds: seeds,
		comm:  comm,
	}
}

func (r *ConvergedRouter) GetRoute(isMutation bool) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.leaderAddr == "" {
		return "", fmt.Errorf("no known route available")
	}

	return r.leaderAddr, nil
}

func (r *ConvergedRouter) Invalidate(failedAddr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leaderAddr == failedAddr {
		r.leaderAddr = ""
	}
}

func (r *ConvergedRouter) SetRouteHint(hint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaderAddr = hint
	r.lastUpdate = time.Now()
}

func (r *ConvergedRouter) fetchTopologyFromSeeds(ctx context.Context) (string, []string, error) {
	for _, seed := range r.seeds {
		reqBody, err := json.Marshal(MsgTopologyRequest{TopologyType: "converged"})
		if err != nil {
			return "", nil, fmt.Errorf("marshal topology request: %w", err)
		}

		req := communication.Message{
			From:    "sandlib",
			Type:    "topology_request",
			Payload: reqBody,
		}

		resp, err := r.comm.Send(ctx, seed, req)
		if err == nil && resp != nil && resp.Code == communication.CodeOK {
			var topoMsg MsgTopologyResponse
			if err := json.Unmarshal(resp.Body, &topoMsg); err == nil {
				return string(topoMsg.TopologyData), r.seeds, nil
			}
		}
	}

	return "", nil, fmt.Errorf("all seeds failed topology ping")
}

func (r *ConvergedRouter) Refresh(ctx context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	r.mu.RLock()
	isFresh := time.Since(r.lastUpdate) < 1*time.Second
	r.mu.RUnlock()
	if isFresh {
		return nil
	}

	newLeader, newNodes, err := r.fetchTopologyFromSeeds(ctx)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.leaderAddr = newLeader
	r.allNodes = newNodes
	r.lastUpdate = time.Now()
	r.mu.Unlock()

	return nil
}
