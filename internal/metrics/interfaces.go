package metrics

type MetricTags struct {
	Operation  string
	Service    string
	Additional map[string]string
}

type CounterName string
type ObservationName string
type GaugeName string

type MetricsService interface {
	Increment(name CounterName, value float64, tags MetricTags)
	Observe(name ObservationName, value float64, tags MetricTags)
	Gauge(name GaugeName, value float64, tags MetricTags)
}

type NoOpMetricsService struct{}

func (n NoOpMetricsService) Increment(name CounterName, value float64, tags MetricTags) {}

func (n NoOpMetricsService) Observe(name ObservationName, value float64, tags MetricTags) {}

func (n NoOpMetricsService) Gauge(name GaugeName, value float64, tags MetricTags) {}
