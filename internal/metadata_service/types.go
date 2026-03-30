package metadata_service

import (
	"time"

	"github.com/AnishMulay/sandstore/topology/contract"
)

type InodeType = contract.InodeType

const (
	TypeFile      = contract.TypeFile
	TypeDirectory = contract.TypeDirectory
)

type Inode = contract.Inode
type Attributes = contract.Attributes
type FileSystemStats = contract.FileSystemStats
type DirEntry = contract.DirEntry
type DirEntryPlus = contract.DirEntryPlus
type FileSystemInfo = contract.FileSystemInfo

// Superblock stores immutable filesystem-wide configuration and root identity.
type Superblock struct {
	FsID            string
	RootInodeID     string
	ChunkSize       int64
	MaxFilenameSize int
	MaxFileSize     int64
	CreatedAt       time.Time
}

// Dentry stores a directory entry name, target inode, and inode type.
type Dentry struct {
	Name    string
	InodeID string
	Type    InodeType
}
