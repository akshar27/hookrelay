package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds every Prometheus collector the service exports. Delivery/queue
// collectors are populated from M4 onward; HTTP metrics are wired in M1.
type Metrics struct {
	HTTPRequests *prometheus.CounterVec
	HTTPLatency  *prometheus.HistogramVec
}

// NewMetrics registers the collectors on the given registry.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		HTTPRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "hookrelay_http_requests_total",
			Help: "HTTP requests by route and status class.",
		}, []string{"route", "method", "status"}),
		HTTPLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hookrelay_http_request_duration_seconds",
			Help:    "HTTP request latency by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method"}),
	}
}
