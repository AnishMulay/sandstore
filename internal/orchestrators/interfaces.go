package orchestrators

import (
	"context"

	"github.com/AnishMulay/sandstore/internal/domain"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
)

// PlacementStrategy determines "WHO" holds the data (Topology Rules).
type PlacementStrategy interface {
	SelectTargets(ctx context.Context, chunkID string, replicaCount int) ([]domain.ChunkLocation, error)
}

// EndpointResolver determines "WHERE" a logical node currently resides (Transport Layer).
type EndpointResolver interface {
	ResolveEndpoint(ctx context.Context, logicalAlias string) (string, error)
}

// TransactionCoordinator acts as a factory for isolated 2PC state machines.
type TransactionCoordinator interface {
	NewTransaction(txnID string) TxHandle
}

// TxHandle isolates the state of a single request.
type TxHandle interface {
	Init(ctx context.Context, chunkID string, participants []domain.ChunkLocation) error
	Commit(ctx context.Context, metaUpdate *pms.MetadataOperation, participants []domain.ChunkLocation) error
	Abort(ctx context.Context, participants []domain.ChunkLocation) error
}

// ControlPlaneOrchestrator manages the namespace, permissions, and 2PC intents.
type ControlPlaneOrchestrator interface {
	Start() error
	Stop() error

	GetAttr(ctx context.Context, inodeID string) (*pms.Attributes, error)
	SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error)
	LookupPath(ctx context.Context, path string) (string, error)
	Lookup(ctx context.Context, parentInodeID string, name string) (string, error)
	Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error
	Create(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)
	Mkdir(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)
	Remove(ctx context.Context, parentID string, name string) error
	Rmdir(ctx context.Context, parentID string, name string) error
	Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error
	ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error)
	ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error)
	GetFsStat(ctx context.Context) (*pms.FileSystemStats, error)
	GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error)

	PrepareFileWrite(ctx context.Context, inodeID string, offset int64, length int64) (*domain.WriteContext, error)
	CommitFileWrite(ctx context.Context, txnID string, inodeID string, chunkID string, newEOF int64, isNewChunk bool, targets []domain.ChunkLocation) error
	AbortFileWrite(ctx context.Context, txnID string, targets []domain.ChunkLocation) error

	PrepareFileRead(ctx context.Context, inodeID string, offset int64) (*domain.ReadContext, error)
}

// DataPlaneOrchestrator strictly moves bytes to the physical locations calculated by the Control Plane.
type DataPlaneOrchestrator interface {
	ExecuteWrite(ctx context.Context, txnID string, chunkID string, data []byte, targets []domain.ChunkLocation) error
	ExecuteRead(ctx context.Context, chunkID string, targets []domain.ChunkLocation) ([]byte, error)
}
