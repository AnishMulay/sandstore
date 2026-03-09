package orchestrators

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	pcs "github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/domain"
)

type RaftDataPlaneOrchestrator struct {
	comm             communication.Communicator
	endpointResolver EndpointResolver
	chunkSize        int64
	cs               pcs.ChunkService
}

var _ DataPlaneOrchestrator = (*RaftDataPlaneOrchestrator)(nil)

func NewRaftDataPlaneOrchestrator(comm communication.Communicator, endpointResolver EndpointResolver, chunkSize int64, cs pcs.ChunkService) *RaftDataPlaneOrchestrator {
	return &RaftDataPlaneOrchestrator{
		comm:             comm,
		endpointResolver: endpointResolver,
		chunkSize:        chunkSize,
		cs:               cs,
	}
}

func (d *RaftDataPlaneOrchestrator) HandlePrepareChunk(ctx context.Context, txnID string, chunkID string, data []byte, checksum string) error {
	return d.cs.PrepareChunk(ctx, txnID, chunkID, data, checksum)
}

func (d *RaftDataPlaneOrchestrator) HandleCommitChunk(ctx context.Context, txnID string, chunkID string) error {
	return d.cs.CommitChunk(ctx, txnID, chunkID)
}

func (d *RaftDataPlaneOrchestrator) HandleAbortChunk(ctx context.Context, txnID string, chunkID string) error {
	return d.cs.AbortChunk(ctx, txnID, chunkID)
}

func (d *RaftDataPlaneOrchestrator) HandleReadChunk(ctx context.Context, chunkID string) ([]byte, error) {
	return d.cs.ReadChunk(ctx, chunkID)
}

func (d *RaftDataPlaneOrchestrator) HandleDeleteChunk(ctx context.Context, chunkID string) error {
	return d.cs.DeleteChunkLocal(ctx, chunkID)
}

func (d *RaftDataPlaneOrchestrator) HandleLegacyChunkWrite(ctx context.Context, chunkID string, data []byte) error {
	txnID := "legacy-" + chunkID + "-" + time.Now().Format("20060102150405.000000000")
	checksum := d.calculateChecksum(data)
	err := d.cs.PrepareChunk(ctx, txnID, chunkID, data, checksum)
	if err == nil {
		err = d.cs.CommitChunk(ctx, txnID, chunkID)
	}
	return err
}

func (d *RaftDataPlaneOrchestrator) ExecuteWrite(
	ctx context.Context,
	txnID string,
	chunkID string,
	offset int64,
	data []byte,
	targets []domain.ChunkLocation,
	isNewChunk bool,
) error {
	finalData, err := d.prepareWritePayload(ctx, chunkID, offset, data, targets, isNewChunk)
	if err != nil {
		return err
	}

	checksum := d.calculateChecksum(finalData)

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
					Data:     finalData,
					Checksum: checksum,
				},
			}

			addr, err := d.resolveEndpoint(ctx, target)
			if err != nil {
				errCh <- fmt.Errorf("resolve endpoint for node %s failed: %w", target.LogicalNodeAlias, err)
				return
			}

			resp, err := d.comm.Send(ctx, addr, msg)
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

	addr, err := d.resolveEndpoint(ctx, target)
	if err != nil {
		return nil, err
	}

	resp, err := d.comm.Send(ctx, addr, msg)
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

func (d *RaftDataPlaneOrchestrator) prepareWritePayload(
	ctx context.Context,
	chunkID string,
	offset int64,
	data []byte,
	targets []domain.ChunkLocation,
	isNewChunk bool,
) ([]byte, error) {
	writeOffset := offset % d.chunkSize
	requiredLen := int(writeOffset) + len(data)
	isFullOverwrite := writeOffset == 0 && int64(len(data)) == d.chunkSize

	if isNewChunk {
		out := make([]byte, requiredLen)
		copy(out[int(writeOffset):], data)
		return out, nil
	}

	if isFullOverwrite {
		out := make([]byte, len(data))
		copy(out, data)
		return out, nil
	}

	existingData, err := d.ExecuteRead(ctx, chunkID, targets)
	if err != nil {
		return nil, err
	}

	finalLen := len(existingData)
	if requiredLen > finalLen {
		finalLen = requiredLen
	}

	out := make([]byte, finalLen)
	copy(out, existingData)
	copy(out[int(writeOffset):], data)
	return out, nil
}

func (d *RaftDataPlaneOrchestrator) resolveEndpoint(ctx context.Context, target domain.ChunkLocation) (string, error) {
	if d.endpointResolver == nil {
		return target.PhysicalEndpoint, nil
	}

	return d.endpointResolver.ResolveEndpoint(ctx, target.LogicalNodeAlias)
}
