package invalidation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/invalidation"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

type fakeReconcilerCache struct {
	known       []string
	cached      map[string]int64
	invalidated []string
}

func (c *fakeReconcilerCache) KnownTenants() []string { return c.known }

func (c *fakeReconcilerCache) CachedRevision(tenantID string) (int64, bool) {
	revision, ok := c.cached[tenantID]
	return revision, ok
}

func (c *fakeReconcilerCache) InvalidateTenant(tenantID string) {
	c.invalidated = append(c.invalidated, tenantID)
	delete(c.cached, tenantID)
}

type fakeRevisionSource struct {
	revisions map[string]int64
	err       error
}

func (s *fakeRevisionSource) PermissionRevision(_ context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	if s.err != nil {
		return assignmentstore.PermissionRevision{}, false, s.err
	}
	revision, found := s.revisions[tenantID]
	if !found {
		return assignmentstore.PermissionRevision{}, false, nil
	}
	return assignmentstore.PermissionRevision{TenantID: tenantID, Revision: revision}, true, nil
}

// A tenant whose cached revision matches the database must be left alone -
// reconciliation that touched every tenant on every pass would defeat the
// point of caching at all.
func TestReconcileOnceLeavesAMatchingTenantUntouched(t *testing.T) {
	cache := &fakeReconcilerCache{known: []string{"tenant-a"}, cached: map[string]int64{"tenant-a": 5}}
	store := &fakeRevisionSource{revisions: map[string]int64{"tenant-a": 5}}
	reconciler := invalidation.NewReconciler(invalidation.ReconcilerConfig{Cache: cache, Store: store})

	reconciler.ReconcileOnce(context.Background())

	if len(cache.invalidated) != 0 {
		t.Errorf("a matching tenant was invalidated: %v", cache.invalidated)
	}
}

// A dropped Kafka invalidation leaves the cache holding a stale revision
// forever, on its own - this is exactly the case the reconciler exists for.
// One pass must notice the drift and invalidate the tenant.
func TestReconcileOnceRepairsATenantWhoseCachedRevisionDrifted(t *testing.T) {
	cache := &fakeReconcilerCache{known: []string{"tenant-a"}, cached: map[string]int64{"tenant-a": 5}}
	store := &fakeRevisionSource{revisions: map[string]int64{"tenant-a": 7}}

	var drifts []string
	reconciler := invalidation.NewReconciler(invalidation.ReconcilerConfig{
		Cache: cache, Store: store,
		OnDrift: func(tenantID string, cached, actual int64) {
			drifts = append(drifts, tenantID)
			if cached != 5 || actual != 7 {
				t.Errorf("OnDrift(%s) = cached=%d actual=%d, want 5 and 7", tenantID, cached, actual)
			}
		},
	})

	reconciler.ReconcileOnce(context.Background())

	if len(cache.invalidated) != 1 || cache.invalidated[0] != "tenant-a" {
		t.Fatalf("invalidated = %v, want [tenant-a]", cache.invalidated)
	}
	if len(drifts) != 1 {
		t.Errorf("OnDrift was called %d times, want 1", len(drifts))
	}
}

// A tenant with no cached revision at all - a cold cache entry created only
// by a role-permission read, never a revision read - must still be checked
// and repaired if the database disagrees with "nothing cached".
func TestReconcileOnceRepairsATenantWithNoCachedRevision(t *testing.T) {
	cache := &fakeReconcilerCache{known: []string{"tenant-a"}, cached: map[string]int64{}}
	store := &fakeRevisionSource{revisions: map[string]int64{"tenant-a": 3}}
	reconciler := invalidation.NewReconciler(invalidation.ReconcilerConfig{Cache: cache, Store: store})

	reconciler.ReconcileOnce(context.Background())

	if len(cache.invalidated) != 1 {
		t.Errorf("invalidated = %v, want [tenant-a]", cache.invalidated)
	}
}

// A read error for one tenant must not stop the pass from checking the
// tenants after it.
func TestReconcileOnceContinuesPastAReadErrorForOneTenant(t *testing.T) {
	cache := &fakeReconcilerCache{
		known:  []string{"tenant-a", "tenant-b"},
		cached: map[string]int64{"tenant-a": 5, "tenant-b": 1},
	}
	store := &erroringOneTenantSource{failFor: "tenant-a", revisions: map[string]int64{"tenant-b": 9}}

	var errs []error
	reconciler := invalidation.NewReconciler(invalidation.ReconcilerConfig{
		Cache: cache, Store: store,
		OnError: func(err error) { errs = append(errs, err) },
	})

	reconciler.ReconcileOnce(context.Background())

	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1 (for tenant-a)", len(errs))
	}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != "tenant-b" {
		t.Errorf("invalidated = %v, want [tenant-b] (tenant-b's drift is still repaired)", cache.invalidated)
	}
}

type erroringOneTenantSource struct {
	failFor   string
	revisions map[string]int64
}

func (s *erroringOneTenantSource) PermissionRevision(_ context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	if tenantID == s.failFor {
		return assignmentstore.PermissionRevision{}, false, errors.New("connection reset")
	}
	revision, found := s.revisions[tenantID]
	return assignmentstore.PermissionRevision{TenantID: tenantID, Revision: revision}, found, nil
}

// A tenant this replica has never served costs nothing: KnownTenants would
// simply never name it, so Run must not require any external enumeration of
// every tenant in the system.
func TestReconcileOnceChecksOnlyKnownTenants(t *testing.T) {
	cache := &fakeReconcilerCache{known: nil, cached: map[string]int64{}}
	store := &fakeRevisionSource{revisions: map[string]int64{"tenant-never-served": 100}}
	reconciler := invalidation.NewReconciler(invalidation.ReconcilerConfig{Cache: cache, Store: store})

	reconciler.ReconcileOnce(context.Background())

	if len(cache.invalidated) != 0 {
		t.Errorf("a never-served tenant was touched: %v", cache.invalidated)
	}
}

// Run must reconcile immediately on start, not wait a whole interval, and
// must stop once its context is cancelled.
func TestRunReconcilesImmediatelyThenStopsOnCancellation(t *testing.T) {
	cache := &fakeReconcilerCache{known: []string{"tenant-a"}, cached: map[string]int64{"tenant-a": 5}}
	store := &fakeRevisionSource{revisions: map[string]int64{"tenant-a": 9}}
	reconciler := invalidation.NewReconciler(invalidation.ReconcilerConfig{
		Cache: cache, Store: store, Interval: time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(done)
	}()

	deadline := time.After(time.Second)
	for {
		if len(cache.invalidated) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not reconcile immediately on start")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
