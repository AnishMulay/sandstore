package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

type HyperconvergedRouter struct {
	seeds []string
	comm  communication.Communicator

	mu         sync.RWMutex
	leaderAddr string
	allNodes   []string
	lastUpdate time.Time

	refreshMu sync.Mutex
}

func NewHyperconvergedRouter(seeds []string, comm communication.Communicator) *HyperconvergedRouter {
	return &HyperconvergedRouter{
		seeds: seeds,
		comm:  comm,
	}
}

func (r *HyperconvergedRouter) GetRoute(isMutation bool) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.leaderAddr == "" {
		return "", fmt.Errorf("no known route available")
	}

	return r.leaderAddr, nil
}

func (r *HyperconvergedRouter) Invalidate(failedAddr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leaderAddr == failedAddr {
		r.leaderAddr = ""
	}
}

func (r *HyperconvergedRouter) SetRouteHint(hint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaderAddr = hint
	r.lastUpdate = time.Now()
}

func (r *HyperconvergedRouter) fetchTopologyFromSeeds(ctx context.Context) (string, []string, error) {
	for _, seed := range r.seeds {
		req := communication.Message{
			From:    "sandlib",
			Type:    "topology_request",
			Payload: MsgTopologyRequest{TopologyType: "converged"},
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

func (r *HyperconvergedRouter) Refresh(ctx context.Context) error {
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
