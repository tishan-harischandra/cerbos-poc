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
