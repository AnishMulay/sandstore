package orchestrators

import (
	"context"
	"errors"
	"time"

	"github.com/AnishMulay/sandstore/internal/domain"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/google/uuid"
)

const defaultChunkSize int64 = 8 * 1024 * 1024

var (
	ErrCrossChunkWrite = errors.New("write spans multiple chunks")
	ErrInvalidLength   = errors.New("invalid length")
	ErrIsDirectory     = errors.New("operation not permitted on a directory")
)

type controlPlaneOrchestrator struct {
	metadataService   pms.MetadataService
	placementStrategy PlacementStrategy
	txnCoordinator    TransactionCoordinator
	chunkSize         int64
	replicaCount      int
}

var _ ControlPlaneOrchestrator = (*controlPlaneOrchestrator)(nil)

func NewControlPlaneOrchestrator(
	metadataService pms.MetadataService,
	placementStrategy PlacementStrategy,
	txnCoordinator TransactionCoordinator,
	chunkSize int64,
	replicaCount int,
) *controlPlaneOrchestrator {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	if replicaCount <= 0 {
		replicaCount = defaultReplicaCount
	}

	return &controlPlaneOrchestrator{
		metadataService:   metadataService,
		placementStrategy: placementStrategy,
		txnCoordinator:    txnCoordinator,
		chunkSize:         chunkSize,
		replicaCount:      replicaCount,
	}
}

func (c *controlPlaneOrchestrator) Start() error {
	if err := c.metadataService.Start(); err != nil {
		return err
	}

	if c.chunkSize <= 0 {
		info, err := c.metadataService.GetFsInfo(context.Background())
		if err != nil {
			return err
		}
		c.chunkSize = info.ChunkSize
	}

	return nil
}

func (c *controlPlaneOrchestrator) Stop() error {
	return c.metadataService.Stop()
}

func (c *controlPlaneOrchestrator) PrepareFileWrite(ctx context.Context, inodeID string, offset int64, length int64) (*domain.WriteContext, error) {
	if length < 0 {
		return nil, ErrInvalidLength
	}
	if !c.isWithinChunkBoundary(offset, length) {
		return nil, ErrCrossChunkWrite
	}

	inode, err := c.metadataService.GetInode(ctx, inodeID)
	if err != nil {
		return nil, err
	}
	if inode.Type != pms.TypeFile {
		return nil, ErrIsDirectory
	}

	chunkIdx := offset / c.chunkSize
	isNewChunk := int(chunkIdx) >= len(inode.ChunkList)

	var chunkID string
	var targets []domain.ChunkLocation

	if isNewChunk {
		chunkID = uuid.NewString()
		targets, err = c.placementStrategy.SelectTargets(ctx, chunkID, c.replicaCount)
		if err != nil {
			return nil, err
		}
	} else {
		chunkID = inode.ChunkList[chunkIdx].ChunkID
		targets = inode.ChunkList[chunkIdx].Locations
	}

	txnID := uuid.NewString()
	handle := c.txnCoordinator.NewTransaction(txnID)
	if err := handle.Init(ctx, chunkID, targets); err != nil {
		return nil, err
	}

	return &domain.WriteContext{
		TxnID:       txnID,
		ChunkID:     chunkID,
		TargetNodes: targets,
		IsNewChunk:  isNewChunk,
	}, nil
}

func (c *controlPlaneOrchestrator) CommitFileWrite(
	ctx context.Context,
	txnID string,
	inodeID string,
	chunkID string,
	newEOF int64,
	isNewChunk bool,
	targets []domain.ChunkLocation,
) error {
	handle := c.txnCoordinator.NewTransaction(txnID)

	inode, err := c.metadataService.GetInode(ctx, inodeID)
	if err != nil {
		return err
	}

	newChunkList := make([]domain.ChunkDescriptor, len(inode.ChunkList))
	copy(newChunkList, inode.ChunkList)

	if isNewChunk {
		newChunkList = append(newChunkList, domain.ChunkDescriptor{
			ChunkID:   chunkID,
			Locations: targets,
		})
	}

	size := inode.FileSize
	if newEOF > size {
		size = newEOF
	}
	mtime := time.Now().UnixNano()

	metaUpdate := &pms.MetadataOperation{
		Type:         pms.OpUpdateInode,
		InodeID:      inode.InodeID,
		NewSize:      &size,
		NewChunkList: newChunkList,
		SetMTime:     &mtime,
		OpID:         uuid.NewString(),
		Timestamp:    mtime,
	}

	return handle.Commit(ctx, chunkID, metaUpdate, targets)
}

func (c *controlPlaneOrchestrator) AbortFileWrite(ctx context.Context, txnID string, chunkID string, targets []domain.ChunkLocation) error {
	handle := c.txnCoordinator.NewTransaction(txnID)
	return handle.Abort(ctx, chunkID, targets)
}

func (c *controlPlaneOrchestrator) PrepareFileRead(ctx context.Context, inodeID string, offset int64) (*domain.ReadContext, error) {
	inode, err := c.metadataService.GetInode(ctx, inodeID)
	if err != nil {
		return nil, err
	}

	chunkIdx := offset / c.chunkSize
	if int(chunkIdx) >= len(inode.ChunkList) {
		return nil, errors.New("read past EOF")
	}

	targetChunk := inode.ChunkList[chunkIdx]

	return &domain.ReadContext{
		ChunkID:     targetChunk.ChunkID,
		TargetNodes: targetChunk.Locations,
	}, nil
}

func (c *controlPlaneOrchestrator) GetAttr(ctx context.Context, inodeID string) (*pms.Attributes, error) {
	return c.metadataService.GetAttributes(ctx, inodeID)
}

func (c *controlPlaneOrchestrator) SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error) {
	return c.metadataService.SetAttributes(ctx, inodeID, mode, uid, gid, atime, mtime)
}

func (c *controlPlaneOrchestrator) LookupPath(ctx context.Context, path string) (string, error) {
	return c.metadataService.LookupPath(ctx, path)
}

func (c *controlPlaneOrchestrator) Lookup(ctx context.Context, parentInodeID string, name string) (string, error) {
	return c.metadataService.Lookup(ctx, parentInodeID, name)
}

func (c *controlPlaneOrchestrator) Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error {
	return c.metadataService.Access(ctx, inodeID, uid, gid, accessMask)
}

func (c *controlPlaneOrchestrator) Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	return c.metadataService.Create(ctx, parentInodeID, name, mode, uid, gid)
}

func (c *controlPlaneOrchestrator) Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	return c.metadataService.Mkdir(ctx, parentInodeID, name, mode, uid, gid)
}

func (c *controlPlaneOrchestrator) Remove(ctx context.Context, parentInodeID string, name string) error {
	return c.metadataService.Remove(ctx, parentInodeID, name)
}

func (c *controlPlaneOrchestrator) Rmdir(ctx context.Context, parentInodeID string, name string) error {
	return c.metadataService.Rmdir(ctx, parentInodeID, name)
}

func (c *controlPlaneOrchestrator) Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error {
	return c.metadataService.Rename(ctx, srcParentID, srcName, dstParentID, dstName)
}

func (c *controlPlaneOrchestrator) ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error) {
	return c.metadataService.ReadDir(ctx, inodeID, cookie, maxEntries)
}

func (c *controlPlaneOrchestrator) ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error) {
	return c.metadataService.ReadDirPlus(ctx, inodeID, cookie, maxEntries)
}

func (c *controlPlaneOrchestrator) GetFsStat(ctx context.Context) (*pms.FileSystemStats, error) {
	return c.metadataService.GetFsStat(ctx)
}

func (c *controlPlaneOrchestrator) GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error) {
	return c.metadataService.GetFsInfo(ctx)
}

func (c *controlPlaneOrchestrator) isWithinChunkBoundary(offset int64, length int64) bool {
	if length <= 0 {
		return true
	}

	startChunk := offset / c.chunkSize
	endChunk := (offset + length - 1) / c.chunkSize
	return startChunk == endChunk
}
