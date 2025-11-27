package server

// Message Type Constants
const (
	// File Operations
	MsgGetAttr     = "getattr"
	MsgSetAttr     = "setattr"
	MsgLookup      = "lookup"
	MsgLookupPath  = "lookuppath"
	MsgAccess      = "access"
	MsgRead        = "read"
	MsgWrite       = "write"
	MsgCreate      = "create"
	MsgMkdir       = "mkdir"
	MsgRemove      = "remove"
	MsgRmdir       = "rmdir"
	MsgRename      = "rename"
	MsgReadDir     = "readdir"
	MsgReadDirPlus = "readdirplus"
	MsgFsStat      = "fsstat"
	MsgFsInfo      = "fsinfo"

	// Raft Operations (for the Replicator)
	MsgRaftRequestVote   = "raft_request_vote"
	MsgRaftAppendEntries = "raft_append_entries"

	// Chunk Operations (for the Replicator)
	MsgChunkWrite  = "chunk_write"
	MsgChunkRead   = "chunk_read"
	MsgChunkDelete = "chunk_delete"
)

// --- Payload Structs ---

type GetAttrRequest struct {
	InodeID string `json:"inodeId"`
}

type SetAttrRequest struct {
	InodeID string  `json:"inodeId"`
	Mode    *uint32 `json:"mode,omitempty"`
	UID     *uint32 `json:"uid,omitempty"`
	GID     *uint32 `json:"gid,omitempty"`
	ATime   *int64  `json:"atime,omitempty"`
	MTime   *int64  `json:"mtime,omitempty"`
}

type LookupRequest struct {
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
}

type LookupPathRequest struct {
	Path string `json:"path"`
}

type AccessRequest struct {
	InodeID    string `json:"inodeId"`
	UID        uint32 `json:"uid"`
	GID        uint32 `json:"gid"`
	AccessMask uint32 `json:"accessMask"`
}

type ReadRequest struct {
	InodeID string `json:"inodeId"`
	Offset  int64  `json:"offset"`
	Length  int64  `json:"length"`
}

type WriteRequest struct {
	InodeID string `json:"inodeId"`
	Offset  int64  `json:"offset"`
	Data    []byte `json:"data"`
}

type CreateRequest struct {
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
	Mode     uint32 `json:"mode"`
	UID      uint32 `json:"uid"`
	GID      uint32 `json:"gid"`
}

type MkdirRequest struct {
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
	Mode     uint32 `json:"mode"`
	UID      uint32 `json:"uid"`
	GID      uint32 `json:"gid"`
}

type RemoveRequest struct {
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
}

type RmdirRequest struct {
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
}

type RenameRequest struct {
	SrcParentID string `json:"srcParentId"`
	SrcName     string `json:"srcName"`
	DstParentID string `json:"dstParentId"`
	DstName     string `json:"dstName"`
}

type ReadDirRequest struct {
	InodeID    string `json:"inodeId"`
	Cookie     int    `json:"cookie"`
	MaxEntries int    `json:"maxEntries"`
}

type ReadDirPlusRequest struct {
	InodeID    string `json:"inodeId"`
	Cookie     int    `json:"cookie"`
	MaxEntries int    `json:"maxEntries"`
}

type FsStatRequest struct{}

type FsInfoRequest struct{}
