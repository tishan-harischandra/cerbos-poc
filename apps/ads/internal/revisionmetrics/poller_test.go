package revisionmetrics_test

import (
	"context"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/revisionmetrics"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

type fakeCache struct {
	tenants []string
	cached  map[string]int64
	haveAny map[string]bool
}

func (f *fakeCache) KnownTenants() []string { return f.tenants }

func (f *fakeCache) CachedRevision(tenantID string) (int64, bool) {
	if !f.haveAny[tenantID] {
		return 0, false
	}
	return f.cached[tenantID], true
}

type fakeRevisionStore struct {
	actual map[string]int64
}

func (f *fakeRevisionStore) PermissionRevision(_ context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	rev, found := f.actual[tenantID]
	return assignmentstore.PermissionRevision{Revision: rev}, found, nil
}

type fakeRevisionMetrics struct {
	cached map[string]int64
	actual map[string]int64
	stale  map[string]float64
	behind map[string]float64
}

func newFakeRevisionMetrics() *fakeRevisionMetrics {
	return &fakeRevisionMetrics{
		cached: map[string]int64{}, actual: map[string]int64{},
		stale: map[string]float64{}, behind: map[string]float64{},
	}
}

func (f *fakeRevisionMetrics) SetCachedRevision(tenant string, rev int64) { f.cached[tenant] = rev }
func (f *fakeRevisionMetrics) SetActualRevision(tenant string, rev int64) { f.actual[tenant] = rev }
func (f *fakeRevisionMetrics) SetStaleSeconds(tenant string, seconds float64) {
	f.stale[tenant] = seconds
}
func (f *fakeRevisionMetrics) SetBehindTarget(tenant string, behind bool) {
	if behind {
		f.behind[tenant] = 1
	} else {
		f.behind[tenant] = 0
	}
}

// A converged tenant reports zero stale seconds and is not behind target.
func TestPollOnce_AConvergedTenantReportsNotBehind(t *testing.T) {
	cache := &fakeCache{
		tenants: []string{"tenant-a"},
		cached:  map[string]int64{"tenant-a": 4},
		haveAny: map[string]bool{"tenant-a": true},
	}
	store := &fakeRevisionStore{actual: map[string]int64{"tenant-a": 4}}
	metrics := newFakeRevisionMetrics()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	poller := revisionmetrics.NewPoller(revisionmetrics.PollerConfig{
		Cache: cache, Store: store, Metrics: metrics, Now: func() time.Time { return now },
	})

	poller.PollOnce(context.Background())

	if metrics.cached["tenant-a"] != 4 || metrics.actual["tenant-a"] != 4 {
		t.Errorf("cached/actual = %d/%d, want 4/4", metrics.cached["tenant-a"], metrics.actual["tenant-a"])
	}
	if metrics.stale["tenant-a"] != 0 {
		t.Errorf("stale seconds = %v, want 0 for a converged tenant", metrics.stale["tenant-a"])
	}
	if metrics.behind["tenant-a"] != 0 {
		t.Errorf("behind target = %v, want 0 for a converged tenant", metrics.behind["tenant-a"])
	}
}

// A drifted tenant is reported behind target, and its stale duration
// grows the longer it stays drifted across polls.
func TestPollOnce_ADriftedTenantReportsGrowingStaleDuration(t *testing.T) {
	cache := &fakeCache{
		tenants: []string{"tenant-a"},
		cached:  map[string]int64{"tenant-a": 3},
		haveAny: map[string]bool{"tenant-a": true},
	}
	store := &fakeRevisionStore{actual: map[string]int64{"tenant-a": 5}}
	metrics := newFakeRevisionMetrics()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	poller := revisionmetrics.NewPoller(revisionmetrics.PollerConfig{
		Cache: cache, Store: store, Metrics: metrics, Now: func() time.Time { return now },
	})

	poller.PollOnce(context.Background())
	if metrics.behind["tenant-a"] != 1 {
		t.Fatalf("behind target = %v, want 1 for a drifted tenant", metrics.behind["tenant-a"])
	}
	if metrics.stale["tenant-a"] != 0 {
		t.Fatalf("stale seconds on first drifted poll = %v, want 0", metrics.stale["tenant-a"])
	}

	now = now.Add(10 * time.Second)
	poller.PollOnce(context.Background())
	if metrics.stale["tenant-a"] != 10 {
		t.Fatalf("stale seconds after 10s still drifted = %v, want 10", metrics.stale["tenant-a"])
	}
}

// Once a drifted tenant converges again, its stale duration resets and it
// is no longer reported behind target.
func TestPollOnce_ATenantThatConvergesAfterDriftingResetsStaleDuration(t *testing.T) {
	cache := &fakeCache{
		tenants: []string{"tenant-a"},
		cached:  map[string]int64{"tenant-a": 3},
		haveAny: map[string]bool{"tenant-a": true},
	}
	store := &fakeRevisionStore{actual: map[string]int64{"tenant-a": 5}}
	metrics := newFakeRevisionMetrics()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	poller := revisionmetrics.NewPoller(revisionmetrics.PollerConfig{
		Cache: cache, Store: store, Metrics: metrics, Now: func() time.Time { return now },
	})

	poller.PollOnce(context.Background())
	now = now.Add(10 * time.Second)
	poller.PollOnce(context.Background())

	cache.cached["tenant-a"] = 5
	now = now.Add(1 * time.Second)
	poller.PollOnce(context.Background())

	if metrics.behind["tenant-a"] != 0 {
		t.Fatalf("behind target after converging = %v, want 0", metrics.behind["tenant-a"])
	}
	if metrics.stale["tenant-a"] != 0 {
		t.Fatalf("stale seconds after converging = %v, want 0", metrics.stale["tenant-a"])
	}
}

// A tenant this replica has never cached anything for has nothing to
// report as behind - matching the reconciler's own treatment.
func TestPollOnce_AnUncachedTenantIsSkipped(t *testing.T) {
	cache := &fakeCache{
		tenants: []string{"tenant-a"},
		haveAny: map[string]bool{"tenant-a": false},
	}
	store := &fakeRevisionStore{actual: map[string]int64{"tenant-a": 5}}
	metrics := newFakeRevisionMetrics()
	poller := revisionmetrics.NewPoller(revisionmetrics.PollerConfig{
		Cache: cache, Store: store, Metrics: metrics, Now: time.Now,
	})

	poller.PollOnce(context.Background())

	if metrics.behind["tenant-a"] != 0 {
		t.Fatalf("behind target for an uncached tenant = %v, want 0", metrics.behind["tenant-a"])
	}
}
