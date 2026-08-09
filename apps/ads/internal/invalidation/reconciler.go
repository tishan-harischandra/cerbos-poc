package invalidation

import (
	"context"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// DefaultReconcileInterval is how often Reconciler.Run compares cached and
// actual revisions when ReconcilerConfig.Interval is left zero.
//
// Kept comfortably under the five-second SLO (§10.3) so a dropped
// invalidation event converges within the objective even when the Kafka
// path never delivers it at all.
const DefaultReconcileInterval = 2 * time.Second

// DefaultPerTenantTimeout bounds a single tenant's Store.PermissionRevision
// call within one reconciliation pass when ReconcilerConfig.PerTenantTimeout
// is left zero.
//
// ReconcileOnce runs on one goroutine and checks every known tenant in
// sequence with no timeout of its own, so a single slow or stuck call - a
// PostgreSQL restart leaving the pool's idle connections stale is the case
// that matters here (issue #26's chaos suite) - would otherwise stall every
// tenant behind it and every future tick until that one call finally
// returns, silently pausing convergence for the whole replica rather than
// just missing one pass for one tenant.
const DefaultPerTenantTimeout = 5 * time.Second

// RevisionSource is the authoritative revision this reconciler compares the
// cache against - PostgreSQL, via assignmentstore.Store, directly. It never
// goes through the cache itself: comparing a cache against itself could
// never find drift.
type RevisionSource interface {
	PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error)
}

// ReconcilerCache is the slice of CachingRoleMatrix the reconciler acts
// through.
type ReconcilerCache interface {
	KnownTenants() []string
	CachedRevision(tenantID string) (int64, bool)
	InvalidateTenant(tenantID string)
}

// ReconcilerConfig holds the reconciler's collaborators.
type ReconcilerConfig struct {
	Cache ReconcilerCache
	Store RevisionSource
	// Interval bounds how often a reconciliation pass runs. Zero means
	// DefaultReconcileInterval.
	Interval time.Duration
	// PerTenantTimeout bounds a single tenant's Store.PermissionRevision
	// call within one pass. Zero means DefaultPerTenantTimeout.
	PerTenantTimeout time.Duration
	// OnDrift, if set, is called whenever a pass finds and repairs a
	// mismatch, naming the tenant and the two revisions that disagreed.
	OnDrift func(tenantID string, cached, actual int64)
	// OnError, if set, is called with any error reading a tenant's actual
	// revision. A read error never stops the pass: the tenants after it
	// are still checked.
	OnError func(error)
}

// Reconciler is §10.3's repair path: periodically compares the highest
// tenant revision each replica has cached against PostgreSQL, and
// invalidates any tenant that disagrees - recovering from a lost Kafka
// message, a rebalance, or a consumer outage without any operator action.
type Reconciler struct {
	cfg ReconcilerConfig
}

// NewReconciler applies ReconcilerConfig's defaults and returns a
// Reconciler.
func NewReconciler(cfg ReconcilerConfig) *Reconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultReconcileInterval
	}
	if cfg.PerTenantTimeout <= 0 {
		cfg.PerTenantTimeout = DefaultPerTenantTimeout
	}
	return &Reconciler{cfg: cfg}
}

// ReconcileOnce runs one pass over every tenant the cache currently knows
// about. A tenant this replica has never served has nothing cached to drift,
// so it costs nothing here - a cold replica converges through ordinary cache
// misses on its first read, not through this pass.
func (r *Reconciler) ReconcileOnce(ctx context.Context) {
	for _, tenantID := range r.cfg.Cache.KnownTenants() {
		actual, found, err := r.checkTenant(ctx, tenantID)
		if err != nil {
			if r.cfg.OnError != nil {
				r.cfg.OnError(err)
			}
			continue
		}

		actualRevision := int64(0)
		if found {
			actualRevision = actual.Revision
		}

		cachedRevision, cached := r.cfg.Cache.CachedRevision(tenantID)
		if cached && cachedRevision == actualRevision {
			continue
		}

		r.cfg.Cache.InvalidateTenant(tenantID)
		if r.cfg.OnDrift != nil {
			r.cfg.OnDrift(tenantID, cachedRevision, actualRevision)
		}
	}
}

// checkTenant reads one tenant's actual revision, bounded by
// PerTenantTimeout so this one call can never stall the whole pass.
func (r *Reconciler) checkTenant(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	tenantCtx, cancel := context.WithTimeout(ctx, r.cfg.PerTenantTimeout)
	defer cancel()
	return r.cfg.Store.PermissionRevision(tenantCtx, tenantID)
}

// Run calls ReconcileOnce immediately, then on every tick of Interval, until
// ctx is done.
func (r *Reconciler) Run(ctx context.Context) {
	r.ReconcileOnce(ctx)

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.ReconcileOnce(ctx)
		}
	}
}
