package assignments

import (
	"context"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// OverrideStore is the slice of the authorization database the override read
// needs. Declared here, at the consumer, for the same reason RoleMatrix is: a
// narrow port is what makes DBOverrides testable without a database.
type OverrideStore interface {
	ActiveUserOverrides(ctx context.Context, query assignmentstore.ActiveUserOverridesQuery) ([]assignmentstore.UserOverride, error)
}

// DBOverrides resolves user overrides from the authorization database.
//
// Like Resolver, it reports facts and nothing else: which action a database
// row grants or revokes for this principal and this resource. A disabled row
// is dropped rather than translated, because §8.3 says a disabled override is
// INHERIT - it is not a fact for Cerbos to weigh, it is the absence of one.
type DBOverrides struct {
	store OverrideStore
	now   func() time.Time
}

// NewDBOverrides builds a database-backed Overrides.
func NewDBOverrides(store OverrideStore, now func() time.Time) *DBOverrides {
	if now == nil {
		now = time.Now
	}
	return &DBOverrides{store: store, now: now}
}

// For resolves the overrides that apply to one principal and one resource.
func (d *DBOverrides) For(ctx context.Context, query authz.AssignmentQuery) ([]permissioncontext.UserOverride, error) {
	found, err := d.store.ActiveUserOverrides(ctx, assignmentstore.ActiveUserOverridesQuery{
		TenantID:           query.TenantID,
		HospitalID:         query.HospitalID,
		UserExternalID:     query.PrincipalID,
		ResourceKey:        query.ResourceKind,
		ResourceInstanceID: query.ResourceID,
		At:                 d.now(),
	})
	if err != nil {
		return nil, fmt.Errorf("reading the user overrides: %w", err)
	}

	overrides := make([]permissioncontext.UserOverride, 0, len(found))
	for _, row := range found {
		if !row.Enabled {
			continue
		}
		state, known := overrideState(row.Effect)
		if !known {
			continue
		}
		overrides = append(overrides, permissioncontext.UserOverride{
			Action: row.Key.ActionKey,
			State:  state,
		})
	}
	return overrides, nil
}

func overrideState(effect assignmentstore.OverrideEffect) (permissioncontext.OverrideState, bool) {
	switch effect {
	case assignmentstore.EffectGrant:
		return permissioncontext.Grant, true
	case assignmentstore.EffectRevoke:
		return permissioncontext.Revoke, true
	default:
		return permissioncontext.Inherit, false
	}
}
