package sandlib

import (
	"sync"

	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
)

// SandstoreFD represents one open file entry in the client-side descriptor table.
//
// Mu protects all mutable per-file state (Offset, Buffer, and FilePath updates)
// so concurrent reads/writes/fsync/close operations on the same descriptor are
// serialized in a deterministic order.
type SandstoreFD struct {
	FD       uint64
	InodeID  string
	FilePath string
	Mode     int
	Offset   int64
	Buffer   []byte
	Mu       sync.Mutex
}

// SandstoreClient holds client-wide state for one sandlib instance.
//
// TableMu protects OpenFiles for descriptor insertion/removal/lookup patterns.
// Per-file state is protected by each SandstoreFD.Mu.
type SandstoreClient struct {
	ServerAddr string
	Comm       *grpccomm.GRPCCommunicator

	OpenFiles map[uint64]*SandstoreFD
	TableMu   sync.RWMutex
}
