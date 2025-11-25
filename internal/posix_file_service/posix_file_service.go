package posix_file_service

import (
	"context"

	pms "github.com/AnishMulay/sandstore/internal/posix_metadata_service"
)

type PosixFileService interface {
	// --- Lifecycle ---
	Start() error
	Stop() error

	// --- 1. GETATTR (User Story #1) ---
	// Retrieves file attributes like size, timestamps, mode.
	GetAttr(ctx context.Context, inodeID string) (*pms.Attributes, error)

	// --- 2. SETATTR (User Story #2) ---
	// Updates specific attributes. Pointers allow partial updates (nil = no change).
	SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error)

	// --- 3. LOOKUP (User Story #3) ---
	// Resolves a full path (e.g., "/home/user") to an InodeID.
	LookupPath(ctx context.Context, path string) (string, error)
	
	// Resolves a single name within a directory (e.g., "file.txt" inside parentInode).
	Lookup(ctx context.Context, parentInodeID string, name string) (string, error)

	// --- 4. ACCESS (User Story #4) ---
	// Verifies if a user has permission to perform an action.
	Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error

	// --- 5. READ (User Story #5) ---
	// Reads data from a file.
	// Logic:
	// 1. Fetch Inode from MetadataService to get ChunkList.
	// 2. Calculate which chunks cover [offset, offset+length].
	// 3. Fetch those chunks from ChunkService.
	// 4. Assemble and return the byte slice.
	Read(ctx context.Context, inodeID string, offset int64, length int64) ([]byte, error)

	// --- 6. WRITE (User Story #6) ---
	// Writes data to a file.
	// Logic:
	// 1. Fetch Inode to get current Size and ChunkList.
	// 2. Calculate chunk splits for the data.
	// 3. For each part:
	//    a. If full chunk overwrite: Write new chunk to ChunkService.
	//    b. If partial chunk: Read old chunk, patch bytes, Write new chunk.
	// 4. Update Inode (Size, ChunkList, MTime) via MetadataService.
	Write(ctx context.Context, inodeID string, offset int64, data []byte) (int64, error)

	// --- 7. CREATE (User Story #7) ---
	// Creates a new regular file and its directory entry.
	Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)

	// --- 8. MKDIR (User Story #8) ---
	// Creates a new directory.
	Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error)

	// --- 9. REMOVE (User Story #9) ---
	// Unlinks a file.
	// Note: This removes the dentry. The Chunk garbage collection might happen async
	// or sync depending on implementation (refcount 0).
	Remove(ctx context.Context, parentInodeID string, name string) error

	// --- 10. RMDIR (User Story #10) ---
	// Removes an empty directory.
	Rmdir(ctx context.Context, parentInodeID string, name string) error

	// --- 11. RENAME (User Story #11) ---
	// Atomically moves/renames a file or directory.
	Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error

	// --- 12. READDIR (User Story #12) ---
	// Returns directory entries for iteration.
	ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error)

	// --- 13. READDIRPLUS (User Story #13) ---
	// Returns directory entries WITH metadata stats (optimization).
	ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error)

	// --- 14. FSSTAT (User Story #14) ---
	// Returns cluster-wide usage statistics.
	GetFsStat(ctx context.Context) (*pms.FileSystemStats, error)

	// --- 15. FSINFO (User Story #15) ---
	// Returns static filesystem parameters (like ChunkSize).
	GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error)
}