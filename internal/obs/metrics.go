package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds every Prometheus collector the service exports.
type Metrics struct {
	HTTPRequests *prometheus.CounterVec
	HTTPLatency  *prometheus.HistogramVec

	AttemptsTotal  *prometheus.CounterVec   // label: outcome
	AttemptLatency *prometheus.HistogramVec // label: outcome

	DeliveriesByStatus *prometheus.GaugeVec // label: status — the queue snapshot
	CircuitsOpen       prometheus.Gauge
}

// NewMetrics registers the collectors on the given registry.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		HTTPRequests: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hookrelay_http_requests_total",
			Help: "HTTP requests by route and status class.",
		}, []string{"route", "method", "status"}),
		HTTPLatency: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hookrelay_http_request_duration_seconds",
			Help:    "HTTP request latency by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method"}),

		AttemptsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hookrelay_delivery_attempts_total",
			Help: "Delivery attempts by outcome.",
		}, []string{"outcome"}),
		AttemptLatency: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hookrelay_delivery_attempt_duration_seconds",
			Help:    "Customer-endpoint response time per attempt, by outcome.",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"outcome"}),

		DeliveriesByStatus: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hookrelay_deliveries",
			Help: "Number of delivery rows in each status (sampled).",
		}, []string{"status"}),
		CircuitsOpen: f.NewGauge(prometheus.GaugeOpts{
			Name: "hookrelay_circuits_open",
			Help: "Number of endpoint circuit breakers currently open (sampled).",
		}),
	}
}
