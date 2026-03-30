package orchestrators

import (
	"context"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/domain"
	raft "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
)

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
	AbortFileWrite(ctx context.Context, txnID string, chunkID string, targets []domain.ChunkLocation) error

	PrepareFileRead(ctx context.Context, inodeID string, offset int64) (*domain.ReadContext, error)

	HandleConsensusRequestVote(ctx context.Context, req raft.RequestVoteArgs) (*raft.RequestVoteReply, error)
	HandleConsensusAppendEntries(ctx context.Context, req communication.AppendEntriesRequest) (*raft.AppendEntriesReply, error)
	HandleConsensusInstallSnapshot(ctx context.Context, req communication.InstallSnapshotRequest) (*raft.InstallSnapshotReply, error)
}

// DataPlaneOrchestrator strictly moves bytes to the physical locations calculated by the Control Plane.
type DataPlaneOrchestrator interface {
	ExecuteWrite(ctx context.Context, txnID string, chunkID string, offset int64, data []byte, targets []domain.ChunkLocation, isNewChunk bool) error
	ExecuteRead(ctx context.Context, chunkID string, targets []domain.ChunkLocation) ([]byte, error)
	HandlePrepareChunk(ctx context.Context, txnID string, chunkID string, data []byte, checksum string) error
	HandleCommitChunk(ctx context.Context, txnID string, chunkID string) error
	HandleAbortChunk(ctx context.Context, txnID string, chunkID string) error
	HandleReadChunk(ctx context.Context, chunkID string) ([]byte, error)
	HandleDeleteChunk(ctx context.Context, chunkID string) error
}
