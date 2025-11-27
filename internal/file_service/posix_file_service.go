package file_service

import (
	"context"

	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
)

type FileService interface {
	// --- Lifecycle ---
	Start() error
	Stop() error

	GetAttr(ctx context.Context, inodeID string) (*pms.Attributes, error)

	SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error)

	LookupPath(ctx context.Context, path string) (string, error)
	
	Lookup(ctx context.Context, parentInodeID string, name string) (string, error)

	Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error

	Read(ctx context.Context, inodeID string, offset int64, length int64) ([]byte, error)

	Write(ctx context.Context, inodeID string, offset int64, data []byte) (int64, error)

	Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)

	Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)

	Remove(ctx context.Context, parentInodeID string, name string) error

	Rmdir(ctx context.Context, parentInodeID string, name string) error

	Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error

	ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error)

	ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error)

	GetFsStat(ctx context.Context) (*pms.FileSystemStats, error)

	GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error)
}
