package metadata_service

// OpType identifies the intent of a replicated log entry.
type OpType int

const (
	OpCreate      OpType = iota // Covers both File and Dir creation
	OpRemove             // Covers Unlink and Rmdir
	OpRename
	OpSetAttr            // For SETATTR
	OpUpdateInode        // For WRITE (Size/Chunks update)
)

// MetadataOperation is the container struct serialized to JSON/Protobuf.
type MetadataOperation struct {
	Type OpType `json:"type"`

	// --- Target Identity ---
	InodeID     string `json:"inodeId,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	Name        string `json:"name,omitempty"`

	// --- For Create/Mkdir ---
	FileType InodeType `json:"fileType,omitempty"` // Distinguishes Mkdir vs Create
	Mode     uint32    `json:"mode,omitempty"`
	UID      uint32    `json:"uid,omitempty"`
	GID      uint32    `json:"gid,omitempty"`

	// --- For Rename ---
	DstParentID string `json:"dstParentId,omitempty"`
	DstName     string `json:"dstName,omitempty"`

	// --- For SetAttr (Pointers allow partial updates) ---
	SetMode  *uint32 `json:"setMode,omitempty"`
	SetUID   *uint32 `json:"setUid,omitempty"`
	SetGID   *uint32 `json:"setGid,omitempty"`
	SetATime *int64  `json:"setAtime,omitempty"`
	SetMTime *int64  `json:"setMtime,omitempty"`

	// --- For UpdateInode (Write Support) ---
	NewSize      *int64   `json:"newSize,omitempty"`
	NewChunkList []string `json:"newChunkList,omitempty"`
	
	// --- Common ---
	OpID      string `json:"opId"`
	Timestamp int64  `json:"timestamp"`
}
