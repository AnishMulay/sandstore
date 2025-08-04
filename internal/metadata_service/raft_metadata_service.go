package metadata_service

import "time"

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
