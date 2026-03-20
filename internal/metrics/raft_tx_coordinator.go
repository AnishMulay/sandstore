package metrics

const (
	RaftTxCoordinatorInitLatency                 ObservationName = "raft_tx_coordinator_init"
	RaftTxCoordinatorCommitLatency               ObservationName = "raft_tx_coordinator_commit"
	RaftTxCoordinatorAbortLatency                ObservationName = "raft_tx_coordinator_abort"
	RaftTxCoordinatorBroadcastCommitAsyncLatency ObservationName = "raft_tx_coordinator_broadcast_commit_async"
	RaftTxCoordinatorBroadcastAbortAsyncLatency  ObservationName = "raft_tx_coordinator_broadcast_abort_async"
)
