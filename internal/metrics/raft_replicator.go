package metrics

const (
	RaftReplicatorReplicateLatency              ObservationName = "raft_replicator_replicate"
	RaftReplicatorHandleAppendEntriesLatency    ObservationName = "raft_replicator_handle_append_entries"
	RaftReplicatorHandleRequestVoteLatency      ObservationName = "raft_replicator_handle_request_vote"
	RaftReplicatorHandleInstallSnapshotLatency  ObservationName = "raft_replicator_handle_install_snapshot"
	RaftReplicatorBroadcastAppendEntriesLatency ObservationName = "raft_replicator_broadcast_append_entries"
	RaftReplicatorReplicateAppendEntriesLatency ObservationName = "raft_replicator_replicate_append_entries"
	RaftReplicatorReplicateSnapshotLatency      ObservationName = "raft_replicator_replicate_snapshot"
	RaftReplicatorApplyLogsLockedLatency        ObservationName = "raft_replicator_apply_logs_locked"
	RaftReplicatorCheckCompactionLatency        ObservationName = "raft_replicator_check_compaction"
)
