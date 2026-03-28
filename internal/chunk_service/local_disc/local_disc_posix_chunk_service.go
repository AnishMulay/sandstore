package localdisc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_replicator"
	"github.com/AnishMulay/sandstore/internal/log_service"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/metrics"
)

var (
	// Chunk operation errors
	ErrChunkWriteFailed  = errors.New("failed to write chunk")
	ErrChunkReadFailed   = errors.New("failed to read chunk")
	ErrChunkDeleteFailed = errors.New("failed to delete chunk")
	ErrChunkNotFound     = errors.New("chunk not found")
)

const preparedEntryTTL = 10 * time.Minute

type PreparedIndexEntry struct {
	TxnID        string
	ChunkID      string
	TempFilePath string
	CreatedAt    int64
}

type LocalDiscChunkService struct {
	baseDir           string
	ls                log_service.LogService
	replicationFactor int
	replicator        chunk_replicator.ChunkReplicator
	ms                pms.MetadataService
	metricsService    metrics.MetricsService

	indexLock     sync.Mutex
	finalizeLock  sync.Mutex
	preparedIndex map[string]PreparedIndexEntry
}

func NewLocalDiscChunkService(
	baseDir string,
	ls log_service.LogService,
	metricsService metrics.MetricsService,
) (*LocalDiscChunkService, error) {
	if err := os.MkdirAll(baseDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("creating chunk storage directory %q: %w", baseDir, err)
	}
	return &LocalDiscChunkService{
		baseDir:        baseDir,
		ls:             ls,
		metricsService: metricsService,
		preparedIndex:  make(map[string]PreparedIndexEntry),
	}, nil
}

func (cs *LocalDiscChunkService) Start() error {
	cs.indexLock.Lock()
	defer cs.indexLock.Unlock()

	if cs.preparedIndex == nil {
		cs.preparedIndex = make(map[string]PreparedIndexEntry)
	}

	entries, err := os.ReadDir(cs.baseDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".tmp")
		parts := strings.Split(name, "_")
		if len(parts) != 2 {
			continue
		}

		chunkID := parts[0]
		txnID := parts[1]
		cs.preparedIndex[chunkID] = PreparedIndexEntry{
			TxnID:        txnID,
			ChunkID:      chunkID,
			TempFilePath: filepath.Join(cs.baseDir, entry.Name()),
			CreatedAt:    time.Now().UnixNano(),
		}
	}

	return nil
}

func (cs *LocalDiscChunkService) SetMetadataService(ms pms.MetadataService) {
	cs.ms = ms
}

func (cs *LocalDiscChunkService) PrepareChunk(ctx context.Context, txnID string, chunkID string, data []byte, expectedChecksum string) error {
	start := time.Now()
	defer func() {
		if cs == nil || cs.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		cs.metricsService.Observe(metrics.ChunkServicePrepareChunkLatency, elapsed, metrics.MetricTags{
			Operation: "prepare_chunk",
			Service:   "LocalDiscChunkService",
		})
	}()

	_ = ctx

	tempFileName := fmt.Sprintf("%s_%s.tmp", chunkID, txnID)
	tempFilePath := filepath.Join(cs.baseDir, tempFileName)

	if _, err := os.Stat(tempFilePath); err == nil {
		existingData, readErr := os.ReadFile(tempFilePath)
		if readErr == nil {
			if len(existingData) == len(data) && cs.calculateChecksum(existingData) == expectedChecksum {
				cs.addToPreparedIndex(txnID, chunkID, tempFilePath)
				return nil
			}
		}
	}

	f, err := os.OpenFile(tempFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write chunk data: %w", err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to fsync temp file: %w", err)
	}

	cs.addToPreparedIndex(txnID, chunkID, tempFilePath)
	return nil
}

func (cs *LocalDiscChunkService) CommitChunk(ctx context.Context, txnID string, chunkID string) error {
	start := time.Now()
	defer func() {
		if cs == nil || cs.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		cs.metricsService.Observe(metrics.ChunkServiceCommitChunkLatency, elapsed, metrics.MetricTags{
			Operation: "commit_chunk",
			Service:   "LocalDiscChunkService",
		})
	}()

	_ = ctx
	cs.finalizeLock.Lock()
	defer cs.finalizeLock.Unlock()

	cs.indexLock.Lock()
	entry, exists := cs.preparedIndex[chunkID]
	cs.indexLock.Unlock()

	finalFilePath := cs.finalChunkPath(chunkID)

	if !exists {
		if _, err := os.Stat(finalFilePath); err == nil {
			return nil
		}
		return fmt.Errorf("transaction not found and chunk not finalized")
	}

	if entry.TxnID != txnID {
		return fmt.Errorf("txnID mismatch")
	}

	if err := os.Rename(entry.TempFilePath, finalFilePath); err != nil {
		return fmt.Errorf("failed to commit chunk via rename: %w", err)
	}

	cs.removeFromPreparedIndex(chunkID)
	return nil
}

func (cs *LocalDiscChunkService) AbortChunk(ctx context.Context, txnID string, chunkID string) error {
	start := time.Now()
	defer func() {
		if cs == nil || cs.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		cs.metricsService.Observe(metrics.ChunkServiceAbortChunkLatency, elapsed, metrics.MetricTags{
			Operation: "abort_chunk",
			Service:   "LocalDiscChunkService",
		})
	}()

	_ = ctx
	cs.finalizeLock.Lock()
	defer cs.finalizeLock.Unlock()

	cs.indexLock.Lock()
	entry, exists := cs.preparedIndex[chunkID]
	cs.indexLock.Unlock()

	if !exists {
		return nil
	}

	if entry.TxnID != txnID {
		return fmt.Errorf("txnID mismatch")
	}

	if err := os.Remove(entry.TempFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete aborted chunk: %w", err)
	}

	cs.removeFromPreparedIndex(chunkID)
	return nil
}

func (cs *LocalDiscChunkService) ReadChunk(ctx context.Context, chunkID string) ([]byte, error) {
	start := time.Now()
	defer func() {
		if cs == nil || cs.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		cs.metricsService.Observe(metrics.ChunkServiceReadChunkLatency, elapsed, metrics.MetricTags{
			Operation: "read_chunk",
			Service:   "LocalDiscChunkService",
		})
	}()

	finalFilePath := cs.finalChunkPath(chunkID)

	if data, err := os.ReadFile(finalFilePath); err == nil {
		return data, nil
	}

	cs.indexLock.Lock()
	entry, exists := cs.preparedIndex[chunkID]
	cs.indexLock.Unlock()
	if !exists {
		return nil, ErrChunkNotFound
	}

	if cs.ms == nil {
		return nil, fmt.Errorf("metadata service is not configured")
	}

	state, err := cs.ms.GetIntentState(ctx, entry.TxnID)
	if err != nil {
		return nil, fmt.Errorf("failed to get intent state: %w", err)
	}

	if state == pms.StateCommitted {
		cs.finalizeLock.Lock()
		defer cs.finalizeLock.Unlock()

		if _, err := os.Stat(finalFilePath); os.IsNotExist(err) {
			if err := os.Rename(entry.TempFilePath, finalFilePath); err != nil {
				return nil, fmt.Errorf("failed to finalize chunk: %w", err)
			}
		}

		cs.removeFromPreparedIndex(chunkID)
		return os.ReadFile(finalFilePath)
	}

	if state == pms.StateAborted || (state == pms.StateUnknown && cs.isExpired(entry.CreatedAt)) {
		_ = os.Remove(entry.TempFilePath)
		cs.removeFromPreparedIndex(chunkID)
		return nil, fmt.Errorf("chunk write was aborted or expired")
	}

	return nil, fmt.Errorf("chunk is currently locked in an active transaction")
}

func (cs *LocalDiscChunkService) DeleteChunkLocal(ctx context.Context, chunkID string) error {
	start := time.Now()
	defer func() {
		if cs == nil || cs.metricsService == nil {
			return
		}
		elapsed := time.Since(start).Seconds()
		cs.metricsService.Observe(metrics.ChunkServiceDeleteChunkLocalLatency, elapsed, metrics.MetricTags{
			Operation: "delete_chunk_local",
			Service:   "LocalDiscChunkService",
		})
	}()

	_ = ctx
	path := cs.finalChunkPath(chunkID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (cs *LocalDiscChunkService) finalChunkPath(chunkID string) string {
	return filepath.Join(cs.baseDir, chunkID)
}

func (cs *LocalDiscChunkService) addToPreparedIndex(txnID string, chunkID string, tempFilePath string) {
	cs.indexLock.Lock()
	defer cs.indexLock.Unlock()
	cs.preparedIndex[chunkID] = PreparedIndexEntry{
		TxnID:        txnID,
		ChunkID:      chunkID,
		TempFilePath: tempFilePath,
		CreatedAt:    time.Now().UnixNano(),
	}
}

func (cs *LocalDiscChunkService) removeFromPreparedIndex(chunkID string) {
	cs.indexLock.Lock()
	defer cs.indexLock.Unlock()
	delete(cs.preparedIndex, chunkID)
}

func (cs *LocalDiscChunkService) calculateChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (cs *LocalDiscChunkService) isExpired(createdAt int64) bool {
	return time.Since(time.Unix(0, createdAt)) > preparedEntryTTL
}
