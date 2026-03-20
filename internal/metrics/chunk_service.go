package metrics

const (
	ChunkServicePrepareChunkLatency     ObservationName = "chunk_service_prepare_chunk"
	ChunkServiceCommitChunkLatency      ObservationName = "chunk_service_commit_chunk"
	ChunkServiceAbortChunkLatency       ObservationName = "chunk_service_abort_chunk"
	ChunkServiceReadChunkLatency        ObservationName = "chunk_service_read_chunk"
	ChunkServiceDeleteChunkLocalLatency ObservationName = "chunk_service_delete_chunk_local"
)
