package prometheus

import (
	"net/http"

	"github.com/AnishMulay/sandstore/internal/metrics"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type PrometheusMetricsService struct {
	port       string
	nodeName   string
	histograms map[metrics.ObservationName]*prometheusclient.HistogramVec
}

func NewPrometheusMetricsService(port string, nodeName string) *PrometheusMetricsService {
	latencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_metadata_service_latency_seconds",
			Help: "Histogram of latency for Sandstore metadata service operations",
		},
		[]string{"operation", "service", "node"},
	)
	hyperconvergedServerLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_hyperconverged_server_latency_seconds",
			Help: "Histogram of latency for Sandstore HyperconvergedServer operations",
		},
		[]string{"operation", "service", "node"},
	)
	controlPlaneLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_control_plane_latency_seconds",
			Help: "Histogram of latency for Sandstore control plane operations",
		},
		[]string{"operation", "service", "node"},
	)
	raftReplicatorLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_raft_replicator_latency_seconds",
			Help: "Histogram of latency for Sandstore raft replicator operations",
		},
		[]string{"operation", "service", "node"},
	)
	raftDataPlaneLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_raft_data_plane_latency_seconds",
			Help: "Histogram of latency for Sandstore raft data plane operations",
		},
		[]string{"operation", "service", "node"},
	)
	chunkServiceLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_chunk_service_latency_seconds",
			Help: "Histogram of latency for Sandstore chunk service operations",
		},
		[]string{"operation", "service", "node"},
	)
	fileLogStoreLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_file_log_store_latency_seconds",
			Help: "Histogram of latency for Sandstore file log store operations",
		},
		[]string{"operation", "service", "node"},
	)
	raftTxCoordinatorLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_raft_tx_coordinator_latency_seconds",
			Help: "Histogram of latency for Sandstore raft transaction coordinator operations",
		},
		[]string{"operation", "service", "node"},
	)
	grpcCommunicatorLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_grpc_communicator_latency_seconds",
			Help: "Histogram of latency for Sandstore grpc communicator operations",
		},
		[]string{"operation", "service", "node"},
	)
	etcdClusterServiceLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_etcd_cluster_service_latency_seconds",
			Help: "Histogram of latency for Sandstore etcd cluster service operations",
		},
		[]string{"operation", "service", "node"},
	)
	placementStrategyLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_placement_strategy_latency_seconds",
			Help: "Histogram of latency for Sandstore placement strategy operations",
		},
		[]string{"operation", "service", "node"},
	)
	fileSnapshotStoreLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_file_snapshot_store_latency_seconds",
			Help: "Histogram of latency for Sandstore file snapshot store operations",
		},
		[]string{"operation", "service", "node"},
	)
	fileStableStoreLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_file_stable_store_latency_seconds",
			Help: "Histogram of latency for Sandstore file stable store operations",
		},
		[]string{"operation", "service", "node"},
	)

	histograms := map[metrics.ObservationName]*prometheusclient.HistogramVec{
		metrics.MetadataOperationLatency:                          latencyHistogram,
		metrics.HyperconvergedServerHandleMessageLatency:          hyperconvergedServerLatencyHistogram,
		metrics.ControlPlanePrepareFileWriteLatency:               controlPlaneLatencyHistogram,
		metrics.ControlPlaneCommitFileWriteLatency:                controlPlaneLatencyHistogram,
		metrics.ControlPlaneAbortFileWriteLatency:                 controlPlaneLatencyHistogram,
		metrics.ControlPlanePrepareFileReadLatency:                controlPlaneLatencyHistogram,
		metrics.ControlPlaneGetAttrLatency:                        controlPlaneLatencyHistogram,
		metrics.ControlPlaneSetAttrLatency:                        controlPlaneLatencyHistogram,
		metrics.ControlPlaneLookupLatency:                         controlPlaneLatencyHistogram,
		metrics.ControlPlaneLookupPathLatency:                     controlPlaneLatencyHistogram,
		metrics.ControlPlaneAccessLatency:                         controlPlaneLatencyHistogram,
		metrics.ControlPlaneCreateLatency:                         controlPlaneLatencyHistogram,
		metrics.ControlPlaneMkdirLatency:                          controlPlaneLatencyHistogram,
		metrics.ControlPlaneRemoveLatency:                         controlPlaneLatencyHistogram,
		metrics.ControlPlaneRmdirLatency:                          controlPlaneLatencyHistogram,
		metrics.ControlPlaneRenameLatency:                         controlPlaneLatencyHistogram,
		metrics.ControlPlaneReadDirLatency:                        controlPlaneLatencyHistogram,
		metrics.ControlPlaneReadDirPlusLatency:                    controlPlaneLatencyHistogram,
		metrics.ControlPlaneGetFsStatLatency:                      controlPlaneLatencyHistogram,
		metrics.ControlPlaneGetFsInfoLatency:                      controlPlaneLatencyHistogram,
		metrics.ControlPlaneHandleConsensusRequestVoteLatency:     controlPlaneLatencyHistogram,
		metrics.ControlPlaneHandleConsensusAppendEntriesLatency:   controlPlaneLatencyHistogram,
		metrics.ControlPlaneHandleConsensusInstallSnapshotLatency: controlPlaneLatencyHistogram,
		metrics.RaftReplicatorReplicateLatency:                    raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorHandleAppendEntriesLatency:          raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorHandleRequestVoteLatency:            raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorHandleInstallSnapshotLatency:        raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorBroadcastAppendEntriesLatency:       raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorReplicateAppendEntriesLatency:       raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorReplicateSnapshotLatency:            raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorApplyLogsLockedLatency:              raftReplicatorLatencyHistogram,
		metrics.RaftReplicatorCheckCompactionLatency:              raftReplicatorLatencyHistogram,
		metrics.RaftDataPlaneExecuteWriteLatency:                  raftDataPlaneLatencyHistogram,
		metrics.RaftDataPlaneExecuteReadLatency:                   raftDataPlaneLatencyHistogram,
		metrics.RaftDataPlaneHandlePrepareChunkLatency:            raftDataPlaneLatencyHistogram,
		metrics.RaftDataPlaneHandleCommitChunkLatency:             raftDataPlaneLatencyHistogram,
		metrics.RaftDataPlaneHandleAbortChunkLatency:              raftDataPlaneLatencyHistogram,
		metrics.RaftDataPlaneHandleReadChunkLatency:               raftDataPlaneLatencyHistogram,
		metrics.RaftDataPlaneHandleDeleteChunkLatency:             raftDataPlaneLatencyHistogram,
		metrics.ChunkServicePrepareChunkLatency:                   chunkServiceLatencyHistogram,
		metrics.ChunkServiceCommitChunkLatency:                    chunkServiceLatencyHistogram,
		metrics.ChunkServiceAbortChunkLatency:                     chunkServiceLatencyHistogram,
		metrics.ChunkServiceReadChunkLatency:                      chunkServiceLatencyHistogram,
		metrics.ChunkServiceDeleteChunkLocalLatency:               chunkServiceLatencyHistogram,
		metrics.FileLogStoreStoreLogsLatency:                      fileLogStoreLatencyHistogram,
		metrics.FileLogStoreGetLogLatency:                         fileLogStoreLatencyHistogram,
		metrics.FileLogStoreLastIndexAndTermLatency:               fileLogStoreLatencyHistogram,
		metrics.FileLogStoreDeleteRangeLatency:                    fileLogStoreLatencyHistogram,
		metrics.RaftTxCoordinatorInitLatency:                      raftTxCoordinatorLatencyHistogram,
		metrics.RaftTxCoordinatorCommitLatency:                    raftTxCoordinatorLatencyHistogram,
		metrics.RaftTxCoordinatorAbortLatency:                     raftTxCoordinatorLatencyHistogram,
		metrics.RaftTxCoordinatorBroadcastCommitAsyncLatency:      raftTxCoordinatorLatencyHistogram,
		metrics.RaftTxCoordinatorBroadcastAbortAsyncLatency:       raftTxCoordinatorLatencyHistogram,
		metrics.GRPCCommunicatorSendLatency:                       grpcCommunicatorLatencyHistogram,
		metrics.GRPCCommunicatorStartLatency:                      grpcCommunicatorLatencyHistogram,
		metrics.GRPCCommunicatorStopLatency:                       grpcCommunicatorLatencyHistogram,
		metrics.EtcdClusterServiceStartLatency:                    etcdClusterServiceLatencyHistogram,
		metrics.EtcdClusterServiceRegisterNodeLatency:             etcdClusterServiceLatencyHistogram,
		metrics.EtcdClusterServiceGetHealthyNodesLatency:          etcdClusterServiceLatencyHistogram,
		metrics.EtcdClusterServiceGetAllNodesLatency:              etcdClusterServiceLatencyHistogram,
		metrics.EtcdClusterServiceSyncStateLatency:                etcdClusterServiceLatencyHistogram,
		metrics.EtcdClusterServiceWatchLatency:                    etcdClusterServiceLatencyHistogram,
		metrics.PlacementStrategySelectTargetsLatency:             placementStrategyLatencyHistogram,
		metrics.FileSnapshotStoreSaveLatency:                      fileSnapshotStoreLatencyHistogram,
		metrics.FileSnapshotStoreLoadLatency:                      fileSnapshotStoreLatencyHistogram,
		metrics.FileStableStoreSetStateLatency:                    fileStableStoreLatencyHistogram,
		metrics.FileStableStoreGetStateLatency:                    fileStableStoreLatencyHistogram,
	}

	return &PrometheusMetricsService{
		port:       port,
		nodeName:   nodeName,
		histograms: histograms,
	}
}

func (p *PrometheusMetricsService) Start() {
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(p.port, nil)
}

func (p *PrometheusMetricsService) Increment(name metrics.CounterName, value float64, tags metrics.MetricTags) {
}

func (p *PrometheusMetricsService) Observe(name metrics.ObservationName, value float64, tags metrics.MetricTags) {
	histogram, exists := p.histograms[name]
	if !exists {
		return
	}

	histogram.WithLabelValues(tags.Operation, tags.Service, p.nodeName).Observe(value)
}

func (p *PrometheusMetricsService) Gauge(name metrics.GaugeName, value float64, tags metrics.MetricTags) {
}
