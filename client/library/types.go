package sandlib

import (
	"sync"

	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
)

// SandstoreFD tracks one open file descriptor in the client-side FD table.
type SandstoreFD struct {
	FD       uint64
	InodeID  string
	FilePath string
	Mode     int
	Offset   int64
	Buffer   []byte
	Mu       sync.Mutex
}

// SandstoreClient stores global client state, including the FD table.
type SandstoreClient struct {
	ServerAddr string
	Comm       *grpccomm.GRPCCommunicator

	OpenFiles map[uint64]*SandstoreFD
	TableMu   sync.RWMutex
}
