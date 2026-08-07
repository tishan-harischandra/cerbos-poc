// Package adsmetrics exports the ADS's §17.1 metric surface as Prometheus
// collectors: decision request rate/outcome/latency by resource and
// action, per-cache hit ratio, database cache-miss query latency and
// connection-pool saturation, and revision gauges (via revisionmetrics.Metrics).
package adsmetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Decision exports the decision path's request rate, outcome and latency,
// labeled by resource and action so neither is lost in an aggregate.
type Decision struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// NewDecision builds the collectors and registers them with reg.
func NewDecision(reg prometheus.Registerer) *Decision {
	d := &Decision{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ads_decision_requests_total",
			Help: "Count of authorization decisions by resource, action and outcome (allow, deny, error).",
		}, []string{"resource", "action", "outcome"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ads_decision_duration_seconds",
			Help:    "Time to serve one authorization decision, by resource and action.",
			Buckets: prometheus.DefBuckets,
		}, []string{"resource", "action"}),
	}
	reg.MustRegister(d.requestsTotal, d.requestDuration)
	return d
}

// Observe records one decision's outcome and latency.
func (d *Decision) Observe(resource, action, outcome string, latency time.Duration) {
	d.requestsTotal.WithLabelValues(resource, action, outcome).Inc()
	d.requestDuration.WithLabelValues(resource, action).Observe(latency.Seconds())
}

// Cache exports per-cache hit and miss counts (§17.1: "not as a single
// aggregate").
type Cache struct {
	hits   *prometheus.CounterVec
	misses *prometheus.CounterVec
}

// NewCache builds the collectors and registers them with reg.
func NewCache(reg prometheus.Registerer) *Cache {
	c := &Cache{
		hits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ads_cache_hits_total",
			Help: "Count of lookups served entirely from memory, by cache.",
		}, []string{"cache"}),
		misses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ads_cache_misses_total",
			Help: "Count of lookups that read through to the database, by cache.",
		}, []string{"cache"}),
	}
	reg.MustRegister(c.hits, c.misses)
	return c
}

// cacheRecorder adapts one named cache's Hit/Miss calls to Cache's
// labeled counters, satisfying assignments.CacheMetrics.
type cacheRecorder struct {
	name string
	c    *Cache
}

func (r cacheRecorder) Hit()  { r.c.hits.WithLabelValues(r.name).Inc() }
func (r cacheRecorder) Miss() { r.c.misses.WithLabelValues(r.name).Inc() }

// For returns the recorder for one named cache, e.g. "role_permissions" or
// "user_overrides".
func (c *Cache) For(name string) interface {
	Hit()
	Miss()
} {
	return cacheRecorder{name: name, c: c}
}

// DB exports the authorization database's cache-miss query latency and
// connection-pool saturation.
type DB struct {
	queryLatency  *prometheus.HistogramVec
	poolAcquired  prometheus.Gauge
	poolIdle      prometheus.Gauge
	poolMax       prometheus.Gauge
}

// NewDB builds the collectors and registers them with reg.
func NewDB(reg prometheus.Registerer) *DB {
	d := &DB{
		queryLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ads_db_cache_miss_query_seconds",
			Help:    "Time to read through to PostgreSQL on a cache miss, by cache.",
			Buckets: prometheus.DefBuckets,
		}, []string{"cache"}),
		poolAcquired: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ads_db_pool_acquired_connections",
			Help: "Connections currently checked out of the pool.",
		}),
		poolIdle: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ads_db_pool_idle_connections",
			Help: "Connections currently idle in the pool.",
		}),
		poolMax: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ads_db_pool_max_connections",
			Help: "The pool's configured maximum connection count.",
		}),
	}
	reg.MustRegister(d.queryLatency, d.poolAcquired, d.poolIdle, d.poolMax)
	return d
}

// ObserveQueryLatency records one cache-miss read-through's duration.
func (d *DB) ObserveQueryLatency(cache string, latency time.Duration) {
	d.queryLatency.WithLabelValues(cache).Observe(latency.Seconds())
}

// SetPoolStats records the pool's current saturation.
func (d *DB) SetPoolStats(acquired, idle, max int32) {
	d.poolAcquired.Set(float64(acquired))
	d.poolIdle.Set(float64(idle))
	d.poolMax.Set(float64(max))
}

// Revision exports §17.1's "current root revision and permission revision
// by replica" and "stale-revision duration and number of replicas behind
// the target revision". Implements revisionmetrics.Metrics.
type Revision struct {
	cachedRevision *prometheus.GaugeVec
	actualRevision *prometheus.GaugeVec
	staleSeconds   *prometheus.GaugeVec
	behindTarget   *prometheus.GaugeVec
	rootPolicy     *prometheus.GaugeVec
}

// NewRevision builds the collectors and registers them with reg.
func NewRevision(reg prometheus.Registerer) *Revision {
	r := &Revision{
		cachedRevision: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ads_permission_revision_cached",
			Help: "This replica's currently cached permission revision, by tenant.",
		}, []string{"tenant"}),
		actualRevision: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ads_permission_revision_actual",
			Help: "The authoritative permission revision in PostgreSQL, by tenant.",
		}, []string{"tenant"}),
		staleSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ads_stale_revision_seconds",
			Help: "How long this replica's cached revision has disagreed with the authoritative one, by tenant. Zero when converged.",
		}, []string{"tenant"}),
		behindTarget: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ads_replica_behind_target",
			Help: "1 if this replica's cached revision currently disagrees with the authoritative one for the tenant, 0 otherwise. Sum across replicas for the cluster-wide count.",
		}, []string{"tenant"}),
		rootPolicy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ads_root_policy_revision_info",
			Help: "Always 1; the currently served root policy revision is the 'revision' label.",
		}, []string{"revision"}),
	}
	reg.MustRegister(r.cachedRevision, r.actualRevision, r.staleSeconds, r.behindTarget, r.rootPolicy)
	return r
}

// SetCachedRevision implements revisionmetrics.Metrics.
func (r *Revision) SetCachedRevision(tenant string, revision int64) {
	r.cachedRevision.WithLabelValues(tenant).Set(float64(revision))
}

// SetActualRevision implements revisionmetrics.Metrics.
func (r *Revision) SetActualRevision(tenant string, revision int64) {
	r.actualRevision.WithLabelValues(tenant).Set(float64(revision))
}

// SetStaleSeconds implements revisionmetrics.Metrics.
func (r *Revision) SetStaleSeconds(tenant string, seconds float64) {
	r.staleSeconds.WithLabelValues(tenant).Set(seconds)
}

// SetBehindTarget implements revisionmetrics.Metrics.
func (r *Revision) SetBehindTarget(tenant string, behind bool) {
	value := 0.0
	if behind {
		value = 1.0
	}
	r.behindTarget.WithLabelValues(tenant).Set(value)
}

// SetRootPolicyRevision records the root policy revision this replica
// currently serves, as a Prometheus info-style gauge.
func (r *Revision) SetRootPolicyRevision(revision string) {
	r.rootPolicy.Reset()
	r.rootPolicy.WithLabelValues(revision).Set(1)
}
