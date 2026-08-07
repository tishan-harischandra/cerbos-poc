package assignments_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

func matrixQuery(tenant string, roles ...string) assignmentstore.ActiveRolePermissionQuery {
	return assignmentstore.ActiveRolePermissionQuery{
		TenantID:        tenant,
		RoleExternalIDs: roles,
		ResourceKey:     "patient_record",
		At:              decidedAt,
	}
}

func newCache(inner assignments.RoleMatrix, clock func() time.Time) *assignments.CachingRoleMatrix {
	return assignments.NewCachingRoleMatrix(assignments.CacheConfig{
		Matrix: inner,
		TTL:    time.Minute,
		Now:    clock,
	})
}

// §11.2: the warm path is served in process and only a miss reaches the
// database. Two identical decisions in a row must therefore cost one read.
func TestASecondIdenticalLookupIsServedWithoutReadingTheDatabase(t *testing.T) {
	inner := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{
			grant("tenant-a", "role-doctor", "read", true),
		},
	}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	first, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor"))
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	second, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor"))
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}

	if len(inner.queries) != 1 {
		t.Errorf("two identical lookups made %d database reads, want 1", len(inner.queries))
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("lookups returned %d and %d permissions, want 1 each", len(first), len(second))
	}
	if second[0].Key.ActionKey != "read" {
		t.Errorf("the cached lookup returned %q, want read", second[0].Key.ActionKey)
	}
}

// The cache is keyed by tenant, role and resource (§11.2). A cache that answered
// one tenant's question with another's would be a tenant isolation defect, and a
// resource-blind key would answer for the wrong resource.
func TestTheCacheKeySeparatesTenantsRolesAndResources(t *testing.T) {
	inner := &recordingMatrix{}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	base := matrixQuery("tenant-a", "role-doctor")
	otherTenant := matrixQuery("tenant-b", "role-doctor")
	otherRole := matrixQuery("tenant-a", "role-nurse")
	otherResource := matrixQuery("tenant-a", "role-doctor")
	otherResource.ResourceKey = "prescription"

	for _, query := range []assignmentstore.ActiveRolePermissionQuery{
		base, otherTenant, otherRole, otherResource, base,
	} {
		if _, err := cache.ActiveRolePermissions(ctx, query); err != nil {
			t.Fatalf("lookup: %v", err)
		}
	}

	if len(inner.queries) != 4 {
		t.Errorf("four distinct keys and one repeat made %d reads, want 4", len(inner.queries))
	}
}

type fakeCacheMetrics struct {
	hits   int
	misses int
}

func (m *fakeCacheMetrics) Hit()  { m.hits++ }
func (m *fakeCacheMetrics) Miss() { m.misses++ }

// §17.1's per-cache hit ratio needs a hit reported for a lookup served
// entirely from memory.
func TestASecondIdenticalLookupReportsAHit(t *testing.T) {
	inner := &recordingMatrix{permissions: []assignmentstore.RolePermission{
		grant("tenant-a", "role-doctor", "read", true),
	}}
	metrics := &fakeCacheMetrics{}
	cache := assignments.NewCachingRoleMatrix(assignments.CacheConfig{
		Matrix: inner, TTL: time.Minute, Now: func() time.Time { return decidedAt }, Metrics: metrics,
	})
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("second lookup: %v", err)
	}

	if metrics.misses != 1 {
		t.Errorf("misses = %d, want 1 (the first, cold lookup)", metrics.misses)
	}
	if metrics.hits != 1 {
		t.Errorf("hits = %d, want 1 (the second, warm lookup)", metrics.hits)
	}
}

// A principal's roles are cached one role at a time, so a second principal
// sharing most of them reads only the roles the cache has never seen.
func TestOnlyTheRolesTheCacheHasNotSeenAreRead(t *testing.T) {
	inner := &recordingMatrix{}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx,
		matrixQuery("tenant-a", "role-doctor", "role-nurse")); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if _, err := cache.ActiveRolePermissions(ctx,
		matrixQuery("tenant-a", "role-nurse", "role-porter")); err != nil {
		t.Fatalf("second lookup: %v", err)
	}

	if len(inner.queries) != 2 {
		t.Fatalf("made %d reads, want 2", len(inner.queries))
	}
	missed := inner.queries[1].RoleExternalIDs
	if len(missed) != 1 || missed[0] != "role-porter" {
		t.Errorf("the second read asked for %v, want only the uncached role-porter", missed)
	}
}

// A permission matrix that never expired would serve a revoked grant forever.
// Targeted invalidation from the outbox arrives with its own slice; until then
// the entry has a bounded life.
func TestACachedEntryIsRereadOnceItsLifetimeHasPassed(t *testing.T) {
	inner := &recordingMatrix{}
	now := decidedAt
	cache := newCache(inner, func() time.Time { return now })
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("lookup after expiry: %v", err)
	}

	if len(inner.queries) != 2 {
		t.Errorf("an expired entry was served from the cache: %d reads, want 2", len(inner.queries))
	}
}

// The TTL bounds staleness for validity windows too, not only for edits: a
// grant that expires between two decisions keeps being served until its entry
// is next read through. This pins that bound so the behaviour is a decision on
// record rather than an accident, and so a future targeted invalidation can be
// seen to tighten it.
func TestAGrantExpiringWithinTheCacheLifetimeIsStillServedUntilItIsReread(t *testing.T) {
	now := decidedAt
	inner := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{
			grant("tenant-a", "role-doctor", "read", true),
		},
	}
	cache := newCache(inner, func() time.Time { return now })
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("first lookup: %v", err)
	}

	// The row goes away underneath the cache, as an expiry would.
	inner.permissions = nil

	now = now.Add(30 * time.Second)
	withinTTL, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor"))
	if err != nil {
		t.Fatalf("lookup within the lifetime: %v", err)
	}
	if len(withinTTL) != 1 {
		t.Errorf("resolved %d permissions within the lifetime, want the cached 1", len(withinTTL))
	}

	now = now.Add(31 * time.Second)
	afterTTL, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor"))
	if err != nil {
		t.Fatalf("lookup after the lifetime: %v", err)
	}
	if len(afterTTL) != 0 {
		t.Errorf("resolved %d permissions after the lifetime, want 0: the expiry was never noticed",
			len(afterTTL))
	}
}

// A failed read must not be remembered as "this role grants nothing". Caching an
// outage would turn a transient failure into a lasting silent denial.
func TestAFailedReadIsNotCached(t *testing.T) {
	inner := &recordingMatrix{err: errors.New("connection refused")}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err == nil {
		t.Fatal("a failed read returned no error")
	}

	inner.err = nil
	inner.permissions = []assignmentstore.RolePermission{grant("tenant-a", "role-doctor", "read", true)}
	found, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor"))
	if err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("the retry resolved %d permissions, want 1: the failure was cached", len(found))
	}
}

// The revision decides which state of the matrix a decision was taken against,
// so it is read through the same cache rather than on every decision.
func TestThePermissionRevisionIsCachedToo(t *testing.T) {
	inner := &recordingMatrix{
		hasRevision: true,
		revision:    assignmentstore.PermissionRevision{TenantID: "tenant-a", Revision: 184},
	}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	for range 3 {
		revision, found, err := cache.PermissionRevision(ctx, "tenant-a")
		if err != nil || !found {
			t.Fatalf("reading the revision: found=%t err=%v", found, err)
		}
		if revision.Revision != 184 {
			t.Fatalf("revision = %d, want 184", revision.Revision)
		}
	}

	if inner.revisions != 1 {
		t.Errorf("three decisions made %d revision reads, want 1", inner.revisions)
	}
}

// §10.1's whole point: invalidating one role must not touch an unrelated
// cache entry. Two roles, two tenants and the revision are all cached here,
// and only the one role named survives untouched by InvalidateRole.
func TestInvalidateRoleDropsOnlyTheNamedRoleInTheNamedTenant(t *testing.T) {
	inner := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{
			grant("tenant-a", "role-doctor", "read", true),
		},
	}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("caching role-doctor in tenant-a: %v", err)
	}
	inner.permissions = []assignmentstore.RolePermission{grant("tenant-a", "role-nurse", "read", true)}
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-nurse")); err != nil {
		t.Fatalf("caching role-nurse in tenant-a: %v", err)
	}
	inner.permissions = []assignmentstore.RolePermission{grant("tenant-b", "role-doctor", "read", true)}
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-b", "role-doctor")); err != nil {
		t.Fatalf("caching role-doctor in tenant-b: %v", err)
	}

	cache.InvalidateRole("tenant-a", "role-doctor")

	// tenant-a/role-doctor must now miss and read through.
	inner.permissions = nil
	inner.queries = nil
	invalidated, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor"))
	if err != nil {
		t.Fatalf("reading the invalidated role: %v", err)
	}
	if len(invalidated) != 0 || len(inner.queries) != 1 {
		t.Fatalf("tenant-a/role-doctor: got %d permissions and %d database reads, want a miss that reads through",
			len(invalidated), len(inner.queries))
	}

	// tenant-a/role-nurse and tenant-b/role-doctor must still be cache hits.
	inner.queries = nil
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-nurse")); err != nil {
		t.Fatalf("reading role-nurse: %v", err)
	}
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-b", "role-doctor")); err != nil {
		t.Fatalf("reading tenant-b/role-doctor: %v", err)
	}
	if len(inner.queries) != 0 {
		t.Errorf("reading the untouched entries made %d database reads, want 0 (they should still be cached)",
			len(inner.queries))
	}
}

// InvalidateRevision must drop only the named tenant's cached revision.
func TestInvalidateRevisionDropsOnlyTheNamedTenant(t *testing.T) {
	inner := &recordingMatrix{
		hasRevision: true,
		revision:    assignmentstore.PermissionRevision{TenantID: "tenant-a", Revision: 1},
	}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	if _, _, err := cache.PermissionRevision(ctx, "tenant-a"); err != nil {
		t.Fatalf("caching tenant-a's revision: %v", err)
	}
	inner.revision = assignmentstore.PermissionRevision{TenantID: "tenant-b", Revision: 7}
	if _, _, err := cache.PermissionRevision(ctx, "tenant-b"); err != nil {
		t.Fatalf("caching tenant-b's revision: %v", err)
	}

	cache.InvalidateRevision("tenant-a")

	if _, cached := cache.CachedRevision("tenant-a"); cached {
		t.Error("tenant-a's revision is still cached after InvalidateRevision")
	}
	if revision, cached := cache.CachedRevision("tenant-b"); !cached || revision != 7 {
		t.Errorf("tenant-b's revision = %d cached=%t, want 7 cached=true (untouched)", revision, cached)
	}
}

// InvalidateTenant is the reconciler's tool: it drops everything cached for
// one tenant, both roles and the revision, and nothing for any other tenant.
func TestInvalidateTenantDropsEveryRoleAndTheRevisionForOneTenantOnly(t *testing.T) {
	inner := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{grant("tenant-a", "role-doctor", "read", true)},
		hasRevision: true,
		revision:    assignmentstore.PermissionRevision{TenantID: "tenant-a", Revision: 1},
	}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("caching tenant-a: %v", err)
	}
	if _, _, err := cache.PermissionRevision(ctx, "tenant-a"); err != nil {
		t.Fatalf("caching tenant-a's revision: %v", err)
	}
	inner.permissions = []assignmentstore.RolePermission{grant("tenant-b", "role-doctor", "read", true)}
	inner.revision = assignmentstore.PermissionRevision{TenantID: "tenant-b", Revision: 9}
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-b", "role-doctor")); err != nil {
		t.Fatalf("caching tenant-b: %v", err)
	}
	if _, _, err := cache.PermissionRevision(ctx, "tenant-b"); err != nil {
		t.Fatalf("caching tenant-b's revision: %v", err)
	}

	cache.InvalidateTenant("tenant-a")

	if _, cached := cache.CachedRevision("tenant-a"); cached {
		t.Error("tenant-a's revision is still cached after InvalidateTenant")
	}
	inner.permissions, inner.queries = nil, nil
	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("reading tenant-a after invalidation: %v", err)
	}
	if len(inner.queries) != 1 {
		t.Error("tenant-a's role permissions were still cached after InvalidateTenant")
	}

	if revision, cached := cache.CachedRevision("tenant-b"); !cached || revision != 9 {
		t.Errorf("tenant-b's revision = %d cached=%t, want 9 cached=true (untouched)", revision, cached)
	}
}

// KnownTenants must report every tenant with a live entry, and nothing else.
func TestKnownTenantsListsEveryTenantWithALiveEntry(t *testing.T) {
	inner := &recordingMatrix{permissions: []assignmentstore.RolePermission{grant("tenant-a", "role-doctor", "read", true)}}
	cache := newCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	if _, err := cache.ActiveRolePermissions(ctx, matrixQuery("tenant-a", "role-doctor")); err != nil {
		t.Fatalf("caching tenant-a: %v", err)
	}
	inner.hasRevision = true
	inner.revision = assignmentstore.PermissionRevision{TenantID: "tenant-b", Revision: 1}
	if _, _, err := cache.PermissionRevision(ctx, "tenant-b"); err != nil {
		t.Fatalf("caching tenant-b's revision: %v", err)
	}

	known := cache.KnownTenants()
	seen := map[string]bool{}
	for _, tenant := range known {
		seen[tenant] = true
	}
	if !seen["tenant-a"] || !seen["tenant-b"] {
		t.Fatalf("KnownTenants = %v, want tenant-a and tenant-b", known)
	}
	if len(known) != 2 {
		t.Errorf("KnownTenants = %v, want exactly tenant-a and tenant-b", known)
	}
}
