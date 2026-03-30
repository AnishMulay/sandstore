package contract

import (
	"context"
)

// ControlPlaneOrchestrator manages the namespace, permissions, and 2PC intents.
type ControlPlaneOrchestrator interface {
	Start() error
	Stop() error

	GetAttr(ctx context.Context, inodeID string) (*Attributes, error)
	SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*Attributes, error)
	LookupPath(ctx context.Context, path string) (string, error)
	Lookup(ctx context.Context, parentInodeID string, name string) (string, error)
	Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error
	Create(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*Inode, error)
	Mkdir(ctx context.Context, parentID string, name string, mode uint32, uid, gid uint32) (*Inode, error)
	Remove(ctx context.Context, parentID string, name string) error
	Rmdir(ctx context.Context, parentID string, name string) error
	Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error
	ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]DirEntry, int, bool, error)
	ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]DirEntryPlus, int, bool, error)
	GetFsStat(ctx context.Context) (*FileSystemStats, error)
	GetFsInfo(ctx context.Context) (*FileSystemInfo, error)

	PrepareFileWrite(ctx context.Context, inodeID string, offset int64, length int64) (*WriteContext, error)
	CommitFileWrite(ctx context.Context, txnID string, inodeID string, chunkID string, newEOF int64, isNewChunk bool, targets []ChunkLocation) error
	AbortFileWrite(ctx context.Context, txnID string, chunkID string, targets []ChunkLocation) error

	PrepareFileRead(ctx context.Context, inodeID string, offset int64) (*ReadContext, error)
}

// DataPlaneOrchestrator strictly moves bytes to the physical locations calculated by the control plane.
type DataPlaneOrchestrator interface {
	ExecuteWrite(ctx context.Context, txnID string, chunkID string, offset int64, data []byte, targets []ChunkLocation, isNewChunk bool) error
	ExecuteRead(ctx context.Context, chunkID string, targets []ChunkLocation) ([]byte, error)
	HandlePrepareChunk(ctx context.Context, txnID string, chunkID string, data []byte, checksum string) error
	HandleCommitChunk(ctx context.Context, txnID string, chunkID string) error
	HandleAbortChunk(ctx context.Context, txnID string, chunkID string) error
	HandleReadChunk(ctx context.Context, chunkID string) ([]byte, error)
	HandleDeleteChunk(ctx context.Context, chunkID string) error
}
