package assignments_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// recordingOverrideStore is the database read DBOverrides depends on. It
// answers from memory and records the query it was asked, the same shape as
// recordingMatrix above: what was asked is as much a part of the contract as
// what came back.
type recordingOverrideStore struct {
	overrides []assignmentstore.UserOverride
	err       error

	queries []assignmentstore.ActiveUserOverridesQuery
}

func (s *recordingOverrideStore) ActiveUserOverrides(_ context.Context, query assignmentstore.ActiveUserOverridesQuery) ([]assignmentstore.UserOverride, error) {
	s.queries = append(s.queries, query)
	if s.err != nil {
		return nil, s.err
	}
	return s.overrides, nil
}

func override(hospital, user, action, instance string, effect assignmentstore.OverrideEffect, enabled bool) assignmentstore.UserOverride {
	return assignmentstore.UserOverride{
		Key: assignmentstore.UserOverrideKey{
			TenantID: "tenant-a", HospitalID: hospital, UserExternalID: user,
			ResourceKey: "patient_record", ActionKey: action, ResourceInstanceID: instance,
		},
		Effect: effect, Enabled: enabled,
	}
}

func newDBOverrides(store assignments.OverrideStore) *assignments.DBOverrides {
	return assignments.NewDBOverrides(store, func() time.Time { return decidedAt })
}

// The database reports a fact per row; DBOverrides' only job is to translate
// that fact into the vocabulary permissioncontext.Assemble reads (§8.3). It
// must not decide which of a grant and a revoke wins - that is precedence,
// and precedence lives in Cerbos policy (§6.3, ADR-003).
func TestADatabaseRevokeBecomesAUserOverrideRevoke(t *testing.T) {
	store := &recordingOverrideStore{overrides: []assignmentstore.UserOverride{
		override("hospital-1", "user-doctor-revoked", "update", assignmentstore.NoResourceInstance,
			assignmentstore.EffectRevoke, true),
	}}

	found, err := newDBOverrides(store).For(context.Background(), doctorQuery())
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(found) != 1 || found[0].Action != "update" || found[0].State != permissioncontext.Revoke {
		t.Errorf("overrides = %+v, want one update revoke", found)
	}
}

func TestADatabaseGrantBecomesAUserOverrideGrant(t *testing.T) {
	store := &recordingOverrideStore{overrides: []assignmentstore.UserOverride{
		override("hospital-1", "user-clerk-granted", "read", assignmentstore.NoResourceInstance,
			assignmentstore.EffectGrant, true),
	}}

	found, err := newDBOverrides(store).For(context.Background(), doctorQuery())
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(found) != 1 || found[0].Action != "read" || found[0].State != permissioncontext.Grant {
		t.Errorf("overrides = %+v, want one read grant", found)
	}
}

// A disabled row is INHERIT (§8.3): the role result stands. It must not
// surface as a grant or a revoke, and the safest way to guarantee that is to
// drop it before it ever reaches permissioncontext.
func TestADisabledOverrideRowIsDroppedAsInherit(t *testing.T) {
	store := &recordingOverrideStore{overrides: []assignmentstore.UserOverride{
		override("hospital-1", "user-doctor", "delete", assignmentstore.NoResourceInstance,
			assignmentstore.EffectGrant, false),
	}}

	found, err := newDBOverrides(store).For(context.Background(), doctorQuery())
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("overrides = %+v, want none for a disabled row", found)
	}
}

// The query the store is asked has to carry the caller's whole scope, not just
// the fields a smaller lookup would need: hospital and resource instance are
// what make the isolation and instance-scoping guarantees hold at the
// database (§8.2, proven in the store contract).
func TestTheQueryCarriesTheCallersFullScope(t *testing.T) {
	store := &recordingOverrideStore{}

	if _, err := newDBOverrides(store).For(context.Background(), doctorQuery()); err != nil {
		t.Fatalf("For: %v", err)
	}

	if len(store.queries) != 1 {
		t.Fatalf("the store was asked %d times, want 1", len(store.queries))
	}
	got := store.queries[0]
	want := assignmentstore.ActiveUserOverridesQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-doctor",
		ResourceKey: "patient_record", ResourceInstanceID: "patient-456", At: decidedAt,
	}
	if got != want {
		t.Errorf("query = %+v, want %+v", got, want)
	}
}

func TestAnUnreachableOverrideStoreIsAnErrorRatherThanNoOverrides(t *testing.T) {
	store := &recordingOverrideStore{err: errors.New("connection refused")}

	if _, err := newDBOverrides(store).For(context.Background(), doctorQuery()); err == nil {
		t.Fatal("For returned no error when the override store was unreachable")
	}
}
