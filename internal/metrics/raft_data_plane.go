package metrics

const (
	RaftDataPlaneExecuteWriteLatency           ObservationName = "raft_data_plane_execute_write"
	RaftDataPlaneExecuteReadLatency            ObservationName = "raft_data_plane_execute_read"
	RaftDataPlaneHandlePrepareChunkLatency     ObservationName = "raft_data_plane_handle_prepare_chunk"
	RaftDataPlaneHandleCommitChunkLatency      ObservationName = "raft_data_plane_handle_commit_chunk"
	RaftDataPlaneHandleAbortChunkLatency       ObservationName = "raft_data_plane_handle_abort_chunk"
	RaftDataPlaneHandleReadChunkLatency        ObservationName = "raft_data_plane_handle_read_chunk"
	RaftDataPlaneHandleDeleteChunkLatency      ObservationName = "raft_data_plane_handle_delete_chunk"
	RaftDataPlaneHandleLegacyChunkWriteLatency ObservationName = "raft_data_plane_handle_legacy_chunk_write"
)
