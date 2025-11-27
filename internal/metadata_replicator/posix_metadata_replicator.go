package metadata_replicator

import "context"

type ApplyFunc func(data []byte) error

type MetadataReplicator interface {
	// Start initializes the replication engine (e.g., starts Raft election timers).
	// It takes an 'applier' callback. The Replicator promises to call this
	// function strictly in order for every committed log entry.
	Start(applier ApplyFunc) error
	Replicate(ctx context.Context, data []byte) error
	Stop() error
}
