// Package assignments resolves which permissions apply to a principal.
//
// Role permissions come from the authorization database through the
// assignmentstore port. User overrides are still seeded in process: the
// override tables, their validity windows and their administration surface
// arrive with the user override slice, and the Overrides port here does not
// change when they do.
//
// Like everything else outside the policy tree, this package deals in facts. It
// reports what a role grants and what a user override says. It never decides
// which of them wins.
package assignments

import (
	"context"
	"strings"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// Seeded principals, one per interesting row of the §19.1 matrix.
//
// They are named for their override state, which is all this file decides. What
// each one's roles grant is now a question for the seeded role matrix, and the
// two are deliberately described in separate places so neither can quietly
// start speaking for the other.
const (
	// DoctorWithNoOverride is the control: whatever its roles grant stands.
	DoctorWithNoOverride = "user-doctor"
	// DoctorWithRevokedUpdate carries the same roles with update revoked, the
	// case Cerbos' cross-role semantics would otherwise get wrong.
	DoctorWithRevokedUpdate = "user-doctor-revoked"
	// ClerkWithUserGrantOnly holds no roles and a single user grant, so an
	// allow can only have come from the override.
	ClerkWithUserGrantOnly = "user-clerk-granted"
	// PrincipalWithNoAssignments holds no roles and no overrides, and so
	// exercises default deny.
	PrincipalWithNoAssignments = "user-unassigned"
)

// SeededOverrides is an in-process Overrides implementation.
type SeededOverrides struct {
	byKey map[overrideKey][]permissioncontext.UserOverride
}

type overrideKey struct {
	tenantID     string
	principalID  string
	resourceKind string
}

// NewSeededOverrides returns the seeded user overrides.
func NewSeededOverrides() *SeededOverrides {
	const tenant = "tenant-a"
	const kind = "patient_record"

	return &SeededOverrides{byKey: map[overrideKey][]permissioncontext.UserOverride{
		{tenant, DoctorWithRevokedUpdate, kind}: {
			{Action: "update", State: permissioncontext.Revoke},
		},
		{tenant, ClerkWithUserGrantOnly, kind}: {
			{Action: "read", State: permissioncontext.Grant},
		},
	}}
}

// For returns the overrides that apply, or none. An absent principal is not an
// error: no override simply means the role result stands (§8.3 INHERIT).
func (o *SeededOverrides) For(_ context.Context, query authz.AssignmentQuery) ([]permissioncontext.UserOverride, error) {
	return o.byKey[overrideKey{
		tenantID:     query.TenantID,
		principalID:  query.PrincipalID,
		resourceKind: query.ResourceKind,
	}], nil
}

// Principals lists the seeded principal IDs, for documentation and smoke tests.
func Principals() []string {
	return []string{
		DoctorWithNoOverride,
		DoctorWithRevokedUpdate,
		ClerkWithUserGrantOnly,
		PrincipalWithNoAssignments,
	}
}

// Describe renders the principals the override seed knows about as a single log
// line.
//
// It is explicitly not a list of who the stack will answer for: role grants come
// from the database now, so the stack answers for anyone presenting a seeded
// role. These are only the principals with an override attached.
func Describe() string {
	return strings.Join(Principals(), ", ")
}
