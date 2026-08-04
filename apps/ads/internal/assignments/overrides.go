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
const (
	// DoctorWithRoleGrants has read and update from a role and no overrides.
	DoctorWithRoleGrants = "user-doctor"
	// DoctorWithRevokedUpdate has the same role grants with update revoked,
	// the case Cerbos' cross-role semantics would otherwise get wrong.
	DoctorWithRevokedUpdate = "user-doctor-revoked"
	// ClerkWithUserGrantOnly has no role grants and a single user grant.
	ClerkWithUserGrantOnly = "user-clerk-granted"
	// PrincipalWithNoAssignments exercises default deny.
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
		DoctorWithRoleGrants,
		DoctorWithRevokedUpdate,
		ClerkWithUserGrantOnly,
		PrincipalWithNoAssignments,
	}
}

// Describe renders the seeded principals as a single log line so an operator can
// see what the demo stack will answer for.
func Describe() string {
	return strings.Join(Principals(), ", ")
}
