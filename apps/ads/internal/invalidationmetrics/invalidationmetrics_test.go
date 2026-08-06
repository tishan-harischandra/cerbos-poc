package invalidationmetrics_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/invalidationmetrics"
)

func TestObserveInvalidationLatencyRecordsToTheHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := invalidationmetrics.New(reg)

	metrics.ObserveInvalidationLatency(250 * time.Millisecond)

	count := testutil.CollectAndCount(reg, "ads_invalidation_processing_seconds")
	if count != 1 {
		t.Fatalf("ads_invalidation_processing_seconds has %d collected series, want 1", count)
	}
}

func TestObserveRevocationLatencyIsASeparateMetricFromInvalidationLatency(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := invalidationmetrics.New(reg)

	metrics.ObserveInvalidationLatency(100 * time.Millisecond)
	metrics.ObserveRevocationLatency(50 * time.Millisecond)

	if got := testutil.CollectAndCount(reg, "ads_revocation_latency_seconds"); got != 1 {
		t.Errorf("ads_revocation_latency_seconds has %d collected series, want 1", got)
	}
	if got := testutil.CollectAndCount(reg, "ads_invalidation_processing_seconds"); got != 1 {
		t.Errorf("ads_invalidation_processing_seconds has %d collected series, want 1", got)
	}
}

func TestSetConsumerLagUpdatesTheGauge(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := invalidationmetrics.New(reg)

	metrics.SetConsumerLag(42)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	var found bool
	for _, family := range families {
		if family.GetName() != "ads_kafka_consumer_lag" {
			continue
		}
		found = true
		if got := family.GetMetric()[0].GetGauge().GetValue(); got != 42 {
			t.Errorf("ads_kafka_consumer_lag = %v, want 42", got)
		}
	}
	if !found {
		t.Fatal("ads_kafka_consumer_lag was not registered")
	}
}
