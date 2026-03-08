package orchestrators

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/domain"
)

type RaftDataPlaneOrchestrator struct {
	comm communication.Communicator
}

var _ DataPlaneOrchestrator = (*RaftDataPlaneOrchestrator)(nil)

func NewRaftDataPlaneOrchestrator(comm communication.Communicator) *RaftDataPlaneOrchestrator {
	return &RaftDataPlaneOrchestrator{comm: comm}
}

func (d *RaftDataPlaneOrchestrator) ExecuteWrite(
	ctx context.Context,
	txnID string,
	chunkID string,
	data []byte,
	targets []domain.ChunkLocation,
) error {
	checksum := d.calculateChecksum(data)

	var wg sync.WaitGroup
	errCh := make(chan error, len(targets))

	for _, target := range targets {
		wg.Add(1)
		go func(target domain.ChunkLocation) {
			defer wg.Done()

			msg := communication.Message{
				From: d.comm.Address(),
				Type: communication.MessageTypePrepareChunk,
				Payload: communication.PrepareChunkRequest{
					TxnID:    txnID,
					ChunkID:  chunkID,
					Data:     data,
					Checksum: checksum,
				},
			}

			resp, err := d.comm.Send(ctx, target.PhysicalEndpoint, msg)
			if err != nil {
				errCh <- fmt.Errorf("prepare to node %s failed: %w", target.LogicalNodeAlias, err)
				return
			}
			if resp.Code != communication.CodeOK {
				errCh <- fmt.Errorf("prepare to node %s returned %s", target.LogicalNodeAlias, resp.Code)
			}
		}(target)
	}

	wg.Wait()
	close(errCh)

	var firstErr error
	failures := 0
	for err := range errCh {
		if firstErr == nil {
			firstErr = err
		}
		failures++
	}
	if failures > 0 {
		return fmt.Errorf("%d/%d prepare RPCs failed: %w", failures, len(targets), firstErr)
	}
	return nil
}

func (d *RaftDataPlaneOrchestrator) ExecuteRead(
	ctx context.Context,
	chunkID string,
	targets []domain.ChunkLocation,
) ([]byte, error) {
	for _, target := range targets {
		data, err := d.sendReadRPC(ctx, target, chunkID)
		if err == nil {
			return data, nil
		}
	}

	return nil, fmt.Errorf("all replicas failed to serve chunk %s", chunkID)
}

func (d *RaftDataPlaneOrchestrator) sendReadRPC(
	ctx context.Context,
	target domain.ChunkLocation,
	chunkID string,
) ([]byte, error) {
	msg := communication.Message{
		From: d.comm.Address(),
		Type: communication.MessageTypeReadChunk,
		Payload: communication.ReadChunkRequest{
			ChunkID: chunkID,
		},
	}

	resp, err := d.comm.Send(ctx, target.PhysicalEndpoint, msg)
	if err != nil {
		return nil, err
	}
	if resp.Code != communication.CodeOK {
		return nil, fmt.Errorf("read from node %s returned %s", target.LogicalNodeAlias, resp.Code)
	}
	return resp.Body, nil
}

func (d *RaftDataPlaneOrchestrator) calculateChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
