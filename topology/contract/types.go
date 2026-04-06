package contract

import "time"

// ChunkLocation identifies a chunk replica by logical node and network endpoint.
type ChunkLocation struct {
	LogicalNodeAlias string
	PhysicalEndpoint string
}

// ChunkDescriptor maps a logical chunk ID to the replica locations that store it.
type ChunkDescriptor struct {
	ChunkID   string
	Locations []ChunkLocation
}

// WriteContext contains the control-plane state required to execute a file write.
type WriteContext struct {
	TxnID       string
	ChunkID     string
	TargetNodes []ChunkLocation
	IsNewChunk  bool
}

// ReadContext contains the control-plane state required to execute a file read.
type ReadContext struct {
	ChunkID     string
	TargetNodes []ChunkLocation
}

// InodeType identifies whether an inode represents a file or a directory.
type InodeType int

const (
	// TypeFile identifies a regular file inode.
	TypeFile InodeType = iota
	// TypeDirectory identifies a directory inode.
	TypeDirectory
)

// Inode is the fundamental metadata unit.
type Inode struct {
	InodeID   string
	Type      InodeType
	LinkCount int
	Mode      uint32
	OwnerUID  uint32
	OwnerGID  uint32

	AccessTime time.Time
	ModifyTime time.Time
	ChangeTime time.Time

	FileSize      int64
	VersionNumber int64
	Children      map[string]string `json:"children,omitempty"`
	ChunkList     []ChunkDescriptor `json:"chunkList,omitempty"`
}

// Attributes is the stable stat-style view of an inode exposed by the contract.
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

// FileSystemStats reports dynamic filesystem capacity and inode usage.
type FileSystemStats struct {
	TotalSpace  int64
	UsedSpace   int64
	TotalInodes int64
	UsedInodes  int64
	BlockSize   int64
}

// DirEntry is a directory listing entry without inode attributes.
type DirEntry struct {
	Name    string
	InodeID string
	Type    InodeType
}

// DirEntryPlus is a directory listing entry with inode attributes attached.
type DirEntryPlus struct {
	Name    string
	InodeID string
	Type    InodeType
	Inode   *Attributes
}

// FileSystemInfo reports static filesystem configuration and limits.
type FileSystemInfo struct {
	FsID            string
	MaxFileSize     int64
	MaxFilenameSize int
	ChunkSize       int64
}
