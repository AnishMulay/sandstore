package posix_metadata_service

import (
	"context"
)

type PosixMetadataService interface {
	// --- Lifecycle ---
	Start() error
	Stop() error

	// --- 1. GETATTR ---
	// GetAttributes retrieves standard metadata (stat).
	GetAttributes(ctx context.Context, inodeID string) (*Attributes, error)

	// --- 2. SETATTR ---
	// SetAttributes updates specific metadata fields (chmod, chown, utimes).
	SetAttributes(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*Attributes, error)

	// --- 3. LOOKUP ---
	// Lookup resolves a child name within a directory to an InodeID.
	Lookup(ctx context.Context, parentInodeID string, name string) (string, error)

	// --- 4. ACCESS ---
	// Access checks if the user (uid/gid) has the requested permission mask for the inode.
	Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error

	// --- 5. READ Support ---
	// GetInode retrieves the FULL inode, including the ChunkList required to read data.
	// This supports the READ operation in the FileService.
	GetInode(ctx context.Context, inodeID string) (*Inode, error)

	// --- 6. WRITE Support ---
	// UpdateInode updates file size, modification time, and the chunk list.
	// This is called by FileService after writing data to the ChunkService.
	UpdateInode(ctx context.Context, inodeID string, newSize int64, newChunkList []string, mtime int64) error

	// --- 7. CREATE ---
	// Create creates a new file and its directory entry atomically.
	Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*Inode, error)

	// --- 8. MKDIR ---
	// Mkdir creates a new directory and its directory entry atomically.
	Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*Inode, error)

	// --- 9. REMOVE ---
	// Remove (Unlink) removes a file's directory entry.
	Remove(ctx context.Context, parentInodeID string, name string) error

	// --- 10. RMDIR ---
	// Rmdir removes a directory's directory entry (if empty).
	Rmdir(ctx context.Context, parentInodeID string, name string) error

	// --- 11. RENAME ---
	// Rename atomically moves a directory entry from source to destination.
	Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error

	// --- 12. READDIR ---
	// ReadDir returns a list of simple directory entries (name, id, type).
	// cookie/token is used for pagination (0 = start).
	ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]DirEntry, int, bool, error)

	// --- 13. READDIRPLUS ---
	// ReadDirPlus returns directory entries WITH their attributes (ls -l optimization).
	ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]DirEntryPlus, int, bool, error)

	// --- 14. FSSTAT ---
	// GetFsStat returns dynamic cluster statistics (usage, free space).
	GetFsStat(ctx context.Context) (*FileSystemStats, error)

	// --- 15. FSINFO ---
	// GetFsInfo returns static filesystem configuration (block size, limits).
	GetFsInfo(ctx context.Context) (*FileSystemInfo, error)
}