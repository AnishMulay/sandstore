package chunk

import "time"

type FileChunk struct {
	ChunkID    string
	Size       int64
	CreatedAt  time.Time
	ModifiedAt time.Time
	Checksum   string
}
