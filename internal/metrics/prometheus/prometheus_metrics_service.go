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
	simpleServerLatencyHistogram := promauto.NewHistogramVec(
		prometheusclient.HistogramOpts{
			Name: "sandstore_simple_server_latency_seconds",
			Help: "Histogram of latency for Sandstore SimpleServer operations",
		},
		[]string{"operation", "service", "node"},
	)

	histograms := map[metrics.ObservationName]*prometheusclient.HistogramVec{
		metrics.MetadataOperationLatency:         latencyHistogram,
		metrics.SimpleServerHandleMessageLatency: simpleServerLatencyHistogram,
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
