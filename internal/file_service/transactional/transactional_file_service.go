package transactional

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	filesvc "github.com/AnishMulay/sandstore/internal/file_service"
	pfsinternal "github.com/AnishMulay/sandstore/internal/file_service/internal"
	"github.com/AnishMulay/sandstore/internal/log_service"
	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	rr "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/google/uuid"
)

const (
	defaultChunkSize    int64 = 8 * 1024 * 1024
	defaultReplicaCount       = 3

	messageTypeChunkPrepare = "chunk_prepare"
	messageTypeChunkCommit  = "chunk_commit"
	messageTypeChunkAbort   = "chunk_abort"
)

var ErrCrossChunkWrite = errors.New("write spans multiple chunks")

type prepareChunkRequest struct {
	TxnID    string
	ChunkID  string
	Data     []byte
	Checksum string
}

type finalizeChunkRequest struct {
	TxnID   string
	ChunkID string
}

type TransactionalFileService struct {
	ms             pms.MetadataService
	comm           communication.Communicator
	raft           pmr.MetadataReplicator
	clusterService cluster_service.ClusterService
	ls             log_service.LogService
	chunkSize      int64
	replicaCount   int
}

var _ filesvc.FileService = (*TransactionalFileService)(nil)

func NewTransactionalFileService(
	ms pms.MetadataService,
	comm communication.Communicator,
	raft pmr.MetadataReplicator,
	clusterService cluster_service.ClusterService,
	ls log_service.LogService,
	chunkSize int64,
) *TransactionalFileService {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}

	return &TransactionalFileService{
		ms:             ms,
		comm:           comm,
		raft:           raft,
		clusterService: clusterService,
		ls:             ls,
		chunkSize:      chunkSize,
		replicaCount:   defaultReplicaCount,
	}
}

func (s *TransactionalFileService) Start() error {
	if err := s.ms.Start(); err != nil {
		return err
	}

	if s.chunkSize <= 0 {
		info, err := s.ms.GetFsInfo(context.Background())
		if err != nil {
			return err
		}
		s.chunkSize = info.ChunkSize
	}

	return nil
}

func (s *TransactionalFileService) Stop() error {
	return s.ms.Stop()
}

func (s *TransactionalFileService) Write(ctx context.Context, inodeID string, offset int64, data []byte) (int64, error) {
	if !s.isWithinChunkBoundary(offset, len(data)) {
		return 0, ErrCrossChunkWrite
	}

	inode, err := s.ms.GetInode(ctx, inodeID)
	if err != nil {
		return 0, err
	}
	if inode.Type != pms.TypeFile {
		return 0, pfsinternal.ErrIsDirectory
	}

	chunkIdx := offset / s.chunkSize
	isNewChunk := int(chunkIdx) >= len(inode.ChunkList)

	var chunkID string
	if isNewChunk {
		chunkID = uuid.NewString()
	} else {
		chunkID = inode.ChunkList[chunkIdx]
	}

	targetNodes, err := s.selectPrepareTargets()
	if err != nil {
		return 0, err
	}

	finalData, err := s.prepareFullChunkBuffer(ctx, chunkID, offset, data, isNewChunk)
	if err != nil {
		return 0, err
	}

	checksum := s.calculateChecksum(finalData)
	txnID := uuid.NewString()

	if err := s.broadcastPrepare(ctx, targetNodes, txnID, chunkID, finalData, checksum); err != nil {
		s.broadcastAbortAsync(txnID, chunkID, extractNodeIDs(targetNodes))
		return 0, fmt.Errorf("prepare phase failed: %w", err)
	}

	envelope := rr.RaftCommandEnvelope{
		Type: rr.PayloadChunkIntent,
		ChunkIntent: &rr.ChunkIntentOperation{
			TxnID:     txnID,
			ChunkID:   chunkID,
			NodeIDs:   extractNodeIDs(targetNodes),
			State:     rr.StateCommitted,
			Timestamp: time.Now().UnixNano(),
		},
	}

	newEOF := offset + int64(len(data))
	if isNewChunk || newEOF > inode.FileSize {
		envelope.PosixMeta = s.buildMetadataUpdate(inode, chunkID, chunkIdx, newEOF)
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal raft envelope: %w", err)
	}
	if err := s.raft.Replicate(ctx, payload); err != nil {
		return 0, fmt.Errorf("consensus commit failed: %w", err)
	}

	s.broadcastCommitAsync(txnID, chunkID, envelope.ChunkIntent.NodeIDs)

	return int64(len(data)), nil
}

func (s *TransactionalFileService) Read(ctx context.Context, inodeID string, offset int64, length int64) ([]byte, error) {
	inode, err := s.ms.GetInode(ctx, inodeID)
	if err != nil {
		return nil, err
	}
	if inode.Type != pms.TypeFile {
		return nil, pfsinternal.ErrIsDirectory
	}

	if offset >= inode.FileSize || length <= 0 {
		return []byte{}, nil
	}
	if offset+length > inode.FileSize {
		length = inode.FileSize - offset
	}

	startChunkIdx := offset / s.chunkSize
	endChunkIdx := (offset + length - 1) / s.chunkSize
	result := make([]byte, length)
	resultOffset := 0

	for i := startChunkIdx; i <= endChunkIdx; i++ {
		if int(i) >= len(inode.ChunkList) {
			break
		}

		chunkID := inode.ChunkList[i]
		nodeIDs, err := s.ms.GetChunkPlacement(ctx, chunkID)
		if err != nil || len(nodeIDs) == 0 {
			return nil, fmt.Errorf("chunk placement not found in metadata")
		}

		chunkData, err := s.readFromPlacement(ctx, nodeIDs, chunkID)
		if err != nil {
			return nil, err
		}

		chunkPos := i * s.chunkSize
		chunkReadStart := int64(0)
		if i == startChunkIdx {
			chunkReadStart = offset - chunkPos
		}

		chunkReadEnd := s.chunkSize
		if i == endChunkIdx {
			chunkReadEnd = (offset + length) - chunkPos
		}

		if chunkReadStart < int64(len(chunkData)) {
			end := chunkReadEnd
			if end > int64(len(chunkData)) {
				end = int64(len(chunkData))
			}
			n := copy(result[resultOffset:], chunkData[chunkReadStart:end])
			resultOffset += n
		}
	}

	return result[:resultOffset], nil
}

func (s *TransactionalFileService) broadcastPrepare(
	ctx context.Context,
	targetNodes []cluster_service.Node,
	txnID string,
	chunkID string,
	data []byte,
	checksum string,
) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(targetNodes))

	for _, node := range targetNodes {
		wg.Add(1)
		go func(node cluster_service.Node) {
			defer wg.Done()

			msg := communication.Message{
				From: s.comm.Address(),
				Type: messageTypeChunkPrepare,
				Payload: prepareChunkRequest{
					TxnID:    txnID,
					ChunkID:  chunkID,
					Data:     data,
					Checksum: checksum,
				},
			}

			resp, err := s.comm.Send(ctx, node.Address, msg)
			if err != nil {
				errCh <- fmt.Errorf("prepare to node %s failed: %w", node.ID, err)
				return
			}
			if resp.Code != communication.CodeOK {
				errCh <- fmt.Errorf("prepare to node %s returned %s", node.ID, resp.Code)
			}
		}(node)
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
		return fmt.Errorf("%d/%d prepare RPCs failed: %w", failures, len(targetNodes), firstErr)
	}
	return nil
}

func (s *TransactionalFileService) broadcastCommitAsync(txnID string, chunkID string, nodeIDs []string) {
	for _, nodeID := range nodeIDs {
		go func(nodeID string) {
			addr, err := s.resolveNodeAddress(context.Background(), nodeID)
			if err != nil {
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			msg := communication.Message{
				From: s.comm.Address(),
				Type: messageTypeChunkCommit,
				Payload: finalizeChunkRequest{
					TxnID:   txnID,
					ChunkID: chunkID,
				},
			}
			_, _ = s.comm.Send(ctx, addr, msg)
		}(nodeID)
	}
}

func (s *TransactionalFileService) broadcastAbortAsync(txnID string, chunkID string, nodeIDs []string) {
	for _, nodeID := range nodeIDs {
		go func(nodeID string) {
			addr, err := s.resolveNodeAddress(context.Background(), nodeID)
			if err != nil {
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			msg := communication.Message{
				From: s.comm.Address(),
				Type: messageTypeChunkAbort,
				Payload: finalizeChunkRequest{
					TxnID:   txnID,
					ChunkID: chunkID,
				},
			}
			_, _ = s.comm.Send(ctx, addr, msg)
		}(nodeID)
	}
}

func (s *TransactionalFileService) calculateChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *TransactionalFileService) prepareFullChunkBuffer(
	ctx context.Context,
	chunkID string,
	offset int64,
	data []byte,
	isNewChunk bool,
) ([]byte, error) {
	writeOffset := offset % s.chunkSize
	requiredLen := int(writeOffset) + len(data)

	if isNewChunk {
		out := make([]byte, requiredLen)
		copy(out[int(writeOffset):], data)
		return out, nil
	}

	if writeOffset == 0 && int64(len(data)) == s.chunkSize {
		out := make([]byte, len(data))
		copy(out, data)
		return out, nil
	}

	existingData, err := s.readSingleChunkForWrite(ctx, chunkID)
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

func (s *TransactionalFileService) readSingleChunkForWrite(ctx context.Context, chunkID string) ([]byte, error) {
	nodeIDs, err := s.ms.GetChunkPlacement(ctx, chunkID)
	if err != nil || len(nodeIDs) == 0 {
		return nil, fmt.Errorf("chunk placement not found in metadata")
	}
	return s.readFromPlacement(ctx, nodeIDs, chunkID)
}

func (s *TransactionalFileService) readFromPlacement(ctx context.Context, nodeIDs []string, chunkID string) ([]byte, error) {
	for _, nodeID := range nodeIDs {
		data, err := s.sendReadRPC(ctx, nodeID, chunkID)
		if err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("all replicas failed to serve chunk %s", chunkID)
}

func (s *TransactionalFileService) sendReadRPC(ctx context.Context, nodeID string, chunkID string) ([]byte, error) {
	addr, err := s.resolveNodeAddress(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	msg := communication.Message{
		From: s.comm.Address(),
		Type: communication.MessageTypeReadChunk,
		Payload: communication.ReadChunkRequest{
			ChunkID: chunkID,
		},
	}

	resp, err := s.comm.Send(ctx, addr, msg)
	if err != nil {
		return nil, err
	}
	if resp.Code != communication.CodeOK {
		return nil, fmt.Errorf("read from node %s returned %s", nodeID, resp.Code)
	}
	return resp.Body, nil
}

func (s *TransactionalFileService) resolveNodeAddress(ctx context.Context, nodeID string) (string, error) {
	nodes, err := s.clusterService.GetAllNodes()
	if err != nil || len(nodes) == 0 {
		nodes, err = s.clusterService.GetHealthyNodes()
		if err != nil {
			return "", err
		}
	}

	for _, node := range nodes {
		if node.ID == nodeID {
			return node.Address, nil
		}
	}

	return "", fmt.Errorf("node %s not found in cluster view", nodeID)
}

func (s *TransactionalFileService) selectPrepareTargets() ([]cluster_service.Node, error) {
	nodes, err := s.clusterService.GetHealthyNodes()
	if err != nil {
		return nil, err
	}
	if len(nodes) < s.replicaCount {
		return nil, fmt.Errorf("insufficient healthy nodes for prepare: need %d have %d", s.replicaCount, len(nodes))
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	targets := make([]cluster_service.Node, s.replicaCount)
	copy(targets, nodes[:s.replicaCount])
	return targets, nil
}

func (s *TransactionalFileService) buildMetadataUpdate(inode *pms.Inode, chunkID string, chunkIdx int64, newEOF int64) *pms.MetadataOperation {
	newChunkList := make([]string, len(inode.ChunkList))
	copy(newChunkList, inode.ChunkList)

	for int64(len(newChunkList)) <= chunkIdx {
		newChunkList = append(newChunkList, "")
	}
	newChunkList[chunkIdx] = chunkID

	newSize := inode.FileSize
	if newEOF > newSize {
		newSize = newEOF
	}
	mtime := time.Now().UnixNano()

	return &pms.MetadataOperation{
		Type:         pms.OpUpdateInode,
		InodeID:      inode.InodeID,
		NewSize:      &newSize,
		NewChunkList: newChunkList,
		SetMTime:     &mtime,
		OpID:         uuid.NewString(),
		Timestamp:    mtime,
	}
}

func (s *TransactionalFileService) isWithinChunkBoundary(offset int64, dataLen int) bool {
	if dataLen <= 0 {
		return true
	}
	startChunk := offset / s.chunkSize
	endChunk := (offset + int64(dataLen) - 1) / s.chunkSize
	return startChunk == endChunk
}

func extractNodeIDs(nodes []cluster_service.Node) []string {
	nodeIDs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}
	return nodeIDs
}

func (s *TransactionalFileService) GetAttr(ctx context.Context, inodeID string) (*pms.Attributes, error) {
	return s.ms.GetAttributes(ctx, inodeID)
}

func (s *TransactionalFileService) SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error) {
	return s.ms.SetAttributes(ctx, inodeID, mode, uid, gid, atime, mtime)
}

func (s *TransactionalFileService) LookupPath(ctx context.Context, path string) (string, error) {
	return s.ms.LookupPath(ctx, path)
}

func (s *TransactionalFileService) Lookup(ctx context.Context, parentInodeID string, name string) (string, error) {
	return s.ms.Lookup(ctx, parentInodeID, name)
}

func (s *TransactionalFileService) Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error {
	return s.ms.Access(ctx, inodeID, uid, gid, accessMask)
}

func (s *TransactionalFileService) Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	return s.ms.Create(ctx, parentInodeID, name, mode, uid, gid)
}

func (s *TransactionalFileService) Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	return s.ms.Mkdir(ctx, parentInodeID, name, mode, uid, gid)
}

func (s *TransactionalFileService) Remove(ctx context.Context, parentInodeID string, name string) error {
	return s.ms.Remove(ctx, parentInodeID, name)
}

func (s *TransactionalFileService) Rmdir(ctx context.Context, parentInodeID string, name string) error {
	return s.ms.Rmdir(ctx, parentInodeID, name)
}

func (s *TransactionalFileService) Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error {
	return s.ms.Rename(ctx, srcParentID, srcName, dstParentID, dstName)
}

func (s *TransactionalFileService) ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error) {
	return s.ms.ReadDir(ctx, inodeID, cookie, maxEntries)
}

func (s *TransactionalFileService) ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error) {
	return s.ms.ReadDirPlus(ctx, inodeID, cookie, maxEntries)
}

func (s *TransactionalFileService) GetFsStat(ctx context.Context) (*pms.FileSystemStats, error) {
	return s.ms.GetFsStat(ctx)
}

func (s *TransactionalFileService) GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error) {
	return s.ms.GetFsInfo(ctx)
}
