package domain

type ChunkLocation struct {
	LogicalNodeAlias string
	PhysicalEndpoint string
}

// ChunkDescriptor bridges the logical ID to its physical locations for the Inode.
type ChunkDescriptor struct {
	ChunkID   string
	Locations []ChunkLocation
}

type WriteContext struct {
	TxnID       string
	ChunkID     string
	TargetNodes []ChunkLocation
	IsNewChunk  bool
	FullPayload []byte // Holds the post-buffered read-modify-write data
}

type ReadContext struct {
	ChunkID     string
	TargetNodes []ChunkLocation
}
