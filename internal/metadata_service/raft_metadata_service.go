package metadata_service

import (
	"sync"
	"time"
)

type MetadataLogEntry struct {
	Index     int64             `json:"index"`
	Term      int64             `json:"term"`
	Type      MetadataOpType    `json:"type"`
	Operation MetadataOperation `json:"operation"`
	Timestamp time.Time         `json:"timestamp"`
}

type MetadataOpType string

const (
	OpTypeCreate MetadataOpType = "create"
	OpTypeDelete MetadataOpType = "delete"
)

type MetadataOperation struct {
	CreateOp *CreateMetadataOp `json:"create_op,omitempty"`
	DeleteOp *DeleteMetadataOp `json:"delete_op,omitempty"`
}

type CreateMetadataOp struct {
	Metadata FileMetadata `json:"metadata"`
}

type DeleteMetadataOp struct {
	Path string `json:"path"`
}

type MetadataLog struct {
	mu          sync.RWMutex
	entries     []MetadataLogEntry
	commitIndex int64
	lastApplied int64
}

func NewMetadataLog() *MetadataLog {
	return &MetadataLog{
		entries:     make([]MetadataLogEntry, 0),
		commitIndex: 0,
		lastApplied: 0,
	}
}
