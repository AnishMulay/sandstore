package metadata

import "time"

type FileMetadata struct {
	Path        string
	Size        int64
	CreatedAt   time.Time
	ModifiedAt  time.Time
	Permissions string
}
