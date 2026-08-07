// Package revisionmetrics polls this replica's cached permission revision
// against the authoritative one in the database and reports §17.1's
// "current root revision and permission revision by replica" and
// "stale-revision duration and number of replicas behind the target
// revision" as Prometheus gauges.
//
// It never invalidates anything itself: that is the reconciler's job
// (apps/ads/internal/invalidation). This package only observes and
// reports what the reconciler either has already repaired or is about
// to.
package revisionmetrics

import (
	"context"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Cache is the slice of assignments.CachingRoleMatrix this poller reads.
type Cache interface {
	KnownTenants() []string
	CachedRevision(tenantID string) (int64, bool)
}

// Store is the authoritative revision source, the same one the reconciler
// compares the cache against.
type Store interface {
	PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error)
}

// Metrics is where a poll reports what it found.
type Metrics interface {
	SetCachedRevision(tenant string, revision int64)
	SetActualRevision(tenant string, revision int64)
	SetStaleSeconds(tenant string, seconds float64)
	SetBehindTarget(tenant string, behind bool)
}

// PollerConfig holds the poller's collaborators.
type PollerConfig struct {
	Cache   Cache
	Store   Store
	Metrics Metrics
	// Now defaults to time.Now.
	Now func() time.Time
}

// Poller periodically compares each known tenant's cached revision
// against the actual one and reports the result as metrics.
type Poller struct {
	cfg PollerConfig
	// driftSince records when a tenant was first observed drifted, so
	// stale duration grows across polls rather than resetting to zero on
	// every tick.
	driftSince map[string]time.Time
}

// NewPoller applies PollerConfig's defaults and returns a Poller.
func NewPoller(cfg PollerConfig) *Poller {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Poller{cfg: cfg, driftSince: make(map[string]time.Time)}
}

// PollOnce runs one pass over every tenant the cache currently knows
// about.
func (p *Poller) PollOnce(ctx context.Context) {
	now := p.cfg.Now()
	for _, tenantID := range p.cfg.Cache.KnownTenants() {
		cachedRevision, cached := p.cfg.Cache.CachedRevision(tenantID)
		if !cached {
			continue
		}

		actual, found, err := p.cfg.Store.PermissionRevision(ctx, tenantID)
		if err != nil {
			continue
		}
		actualRevision := int64(0)
		if found {
			actualRevision = actual.Revision
		}

		p.cfg.Metrics.SetCachedRevision(tenantID, cachedRevision)
		p.cfg.Metrics.SetActualRevision(tenantID, actualRevision)

		if cachedRevision == actualRevision {
			delete(p.driftSince, tenantID)
			p.cfg.Metrics.SetStaleSeconds(tenantID, 0)
			p.cfg.Metrics.SetBehindTarget(tenantID, false)
			continue
		}

		since, drifting := p.driftSince[tenantID]
		if !drifting {
			since = now
			p.driftSince[tenantID] = since
		}
		p.cfg.Metrics.SetStaleSeconds(tenantID, now.Sub(since).Seconds())
		p.cfg.Metrics.SetBehindTarget(tenantID, true)
	}
}

// Run calls PollOnce on every tick of interval until ctx is done.
func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.PollOnce(ctx)
		}
	}
}
