package posix_metadata_service

import (
	"time"
)

type InodeType int

const (
	TypeFile InodeType = iota
	TypeDirectory
)

type Superblock struct {
	FsID            string
	RootInodeID     string
	ChunkSize       int64
	MaxFilenameSize int
	MaxFileSize     int64
	CreatedAt       time.Time
}

// Inode is the fundamental metadata unit.
type Inode struct {
	InodeID   string
	Type      InodeType
	LinkCount int
	Mode      uint32 // Permission bits
	OwnerUID  uint32
	OwnerGID  uint32

	AccessTime time.Time
	ModifyTime time.Time
	ChangeTime time.Time

	FileSize      int64
	VersionNumber int64

	// For Directories: The "Dentry Map"
	Children map[string]string `json:"children,omitempty"`

	// For Files: The "Chunk List"
	ChunkList []string `json:"chunkList,omitempty"`
}

type Dentry struct {
	Name    string
	InodeID string
	Type    InodeType
}

type Attributes struct {
	InodeID    string
	Type       InodeType
	Mode       uint32
	Size       int64
	AccessTime time.Time
	ModifyTime time.Time
	ChangeTime time.Time
	UID        uint32
	GID        uint32
}

type FileSystemStats struct {
	TotalSpace  int64
	UsedSpace   int64
	TotalInodes int64
	UsedInodes  int64
	BlockSize   int64
}

type DirEntry struct {
	Name    string
	InodeID string
	Type    InodeType
}

type DirEntryPlus struct {
	Name    string
	InodeID string
	Type    InodeType
	Inode   *Attributes
}

type FileSystemInfo struct {
	FsID            string
	MaxFileSize     int64
	MaxFilenameSize int
	ChunkSize       int64
}
