// Package invalidationmetrics exports the §10 convergence metrics as
// Prometheus collectors: invalidation processing latency, revocation
// latency measured separately (§10.3's "more critical objective"), and
// Kafka consumer lag.
package invalidationmetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Invalidation implements apps/ads/internal/invalidation.Metrics over
// Prometheus histograms, and additionally exposes consumer lag - which is
// not part of that interface because it is read from the Kafka client
// directly, not observed per event.
type Invalidation struct {
	invalidationLatency prometheus.Histogram
	revocationLatency   prometheus.Histogram
	consumerLag         prometheus.Gauge
}

// New builds the collectors and registers them with reg.
func New(reg prometheus.Registerer) *Invalidation {
	m := &Invalidation{
		invalidationLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "ads_invalidation_processing_seconds",
			Help: "Time between a PermissionChanged event's occurredAt and this replica handling it.",
			Buckets: []float64{
				0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10,
			},
		}),
		revocationLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "ads_revocation_latency_seconds",
			Help: "Time between a permission being revoked and this replica handling the invalidation. Measured separately from ads_invalidation_processing_seconds per §10.3.",
			Buckets: []float64{
				0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10,
			},
		}),
		consumerLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ads_kafka_consumer_lag",
			Help: "Sum of this replica's Kafka consumer group lag across the invalidation topic's partitions.",
		}),
	}
	reg.MustRegister(m.invalidationLatency, m.revocationLatency, m.consumerLag)
	return m
}

// ObserveInvalidationLatency implements invalidation.Metrics.
func (m *Invalidation) ObserveInvalidationLatency(d time.Duration) {
	m.invalidationLatency.Observe(d.Seconds())
}

// ObserveRevocationLatency implements invalidation.Metrics.
func (m *Invalidation) ObserveRevocationLatency(d time.Duration) {
	m.revocationLatency.Observe(d.Seconds())
}

// SetConsumerLag records the Kafka consumer group's current lag.
func (m *Invalidation) SetConsumerLag(lag float64) {
	m.consumerLag.Set(lag)
}
