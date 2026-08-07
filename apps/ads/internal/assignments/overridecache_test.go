package assignments_test

import (
	"context"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

type recordingOverrides struct {
	overrides []permissioncontext.UserOverride
	err       error
	queries   []authz.AssignmentQuery
}

func (r *recordingOverrides) For(_ context.Context, query authz.AssignmentQuery) ([]permissioncontext.UserOverride, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return r.overrides, nil
}

func newOverrideCache(inner assignments.Overrides, clock func() time.Time) *assignments.CachingOverrides {
	return assignments.NewCachingOverrides(assignments.OverrideCacheConfig{
		Overrides: inner,
		TTL:       time.Minute,
		Now:       clock,
	})
}

// §11.2's read-through pattern, applied to user overrides too: a second
// identical lookup must be served from memory.
func TestOverrideCache_ASecondIdenticalLookupIsServedWithoutReadingThrough(t *testing.T) {
	inner := &recordingOverrides{overrides: []permissioncontext.UserOverride{
		{Action: "update", State: permissioncontext.Revoke},
	}}
	cache := newOverrideCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	first, err := cache.For(ctx, doctorQuery())
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	second, err := cache.For(ctx, doctorQuery())
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}

	if len(inner.queries) != 1 {
		t.Errorf("two identical lookups made %d reads through, want 1", len(inner.queries))
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("lookups returned %d and %d overrides, want 1 each", len(first), len(second))
	}
}

// The cache key must separate tenant, hospital, user and resource: a cache
// that answered one user's question with another's, or one tenant's with
// another's, would be a tenant/hospital isolation defect.
func TestOverrideCache_TheKeySeparatesTenantHospitalUserAndResource(t *testing.T) {
	inner := &recordingOverrides{}
	cache := newOverrideCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	base := doctorQuery()
	otherTenant := doctorQuery()
	otherTenant.TenantID = "tenant-b"
	otherHospital := doctorQuery()
	otherHospital.HospitalID = "hospital-2"
	otherUser := doctorQuery()
	otherUser.PrincipalID = "user-nurse"
	otherResource := doctorQuery()
	otherResource.ResourceID = "patient-999"

	for _, query := range []authz.AssignmentQuery{base, otherTenant, otherHospital, otherUser, otherResource, base} {
		if _, err := cache.For(ctx, query); err != nil {
			t.Fatalf("lookup: %v", err)
		}
	}

	if len(inner.queries) != 5 {
		t.Errorf("5 distinct queries made %d reads through, want 5", len(inner.queries))
	}
}

// A stale entry must not outlive the TTL: a revoked override that stayed
// cached past its bound would grant access the database no longer allows.
func TestOverrideCache_AnEntryExpiresAfterTheTTL(t *testing.T) {
	inner := &recordingOverrides{overrides: []permissioncontext.UserOverride{
		{Action: "update", State: permissioncontext.Revoke},
	}}
	now := decidedAt
	cache := newOverrideCache(inner, func() time.Time { return now })
	ctx := context.Background()

	if _, err := cache.For(ctx, doctorQuery()); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := cache.For(ctx, doctorQuery()); err != nil {
		t.Fatalf("second lookup: %v", err)
	}

	if len(inner.queries) != 2 {
		t.Errorf("a lookup past the TTL made %d reads through, want 2", len(inner.queries))
	}
}

// A read-through failure must never be cached: remembering "no overrides"
// after a transient error would turn an outage into a lasting incorrect
// answer.
func TestOverrideCache_AFailedReadThroughIsNeverCached(t *testing.T) {
	inner := &recordingOverrides{err: context.DeadlineExceeded}
	cache := newOverrideCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	if _, err := cache.For(ctx, doctorQuery()); err == nil {
		t.Fatal("For: want error, got nil")
	}
	if len(inner.queries) != 1 {
		t.Fatalf("len(queries) = %d, want 1", len(inner.queries))
	}
}

// InvalidateUser drops every cached entry for one user within one tenant,
// so the outbox consumer's SUBJECT_USER events (§10.1) have something to
// act on.
func TestOverrideCache_InvalidateUserDropsOnlyThatUsersEntries(t *testing.T) {
	inner := &recordingOverrides{overrides: []permissioncontext.UserOverride{
		{Action: "update", State: permissioncontext.Revoke},
	}}
	cache := newOverrideCache(inner, func() time.Time { return decidedAt })
	ctx := context.Background()

	other := doctorQuery()
	other.PrincipalID = "user-nurse"

	if _, err := cache.For(ctx, doctorQuery()); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if _, err := cache.For(ctx, other); err != nil {
		t.Fatalf("lookup: %v", err)
	}

	cache.InvalidateUser(doctorQuery().TenantID, doctorQuery().PrincipalID)

	if _, err := cache.For(ctx, doctorQuery()); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if _, err := cache.For(ctx, other); err != nil {
		t.Fatalf("lookup: %v", err)
	}

	// The invalidated user's entry was re-read; the other user's was not.
	if len(inner.queries) != 3 {
		t.Fatalf("len(queries) = %d, want 3 (2 initial + 1 re-read)", len(inner.queries))
	}
}
