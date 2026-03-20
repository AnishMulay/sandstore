package metrics

const (
	EtcdClusterServiceStartLatency           ObservationName = "etcd_cluster_service_start"
	EtcdClusterServiceRegisterNodeLatency    ObservationName = "etcd_cluster_service_register_node"
	EtcdClusterServiceGetHealthyNodesLatency ObservationName = "etcd_cluster_service_get_healthy_nodes"
	EtcdClusterServiceGetAllNodesLatency     ObservationName = "etcd_cluster_service_get_all_nodes"
	EtcdClusterServiceSyncStateLatency       ObservationName = "etcd_cluster_service_sync_state"
	EtcdClusterServiceWatchLatency           ObservationName = "etcd_cluster_service_watch"
)
