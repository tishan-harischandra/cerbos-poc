// Package assignments resolves which permissions apply to a principal.
//
// This slice seeds them in process. The authorization database, its cache and
// Kafka invalidation arrive with the role matrix slice; the port that the ADS
// depends on does not change when they do.
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

// SeededRevision is the permission revision the fixtures were resolved at.
const SeededRevision int64 = 184

// Fixtures is an in-process Assignments implementation.
type Fixtures struct {
	byKey map[key]permissioncontext.Input
}

type key struct {
	tenantID     string
	principalID  string
	resourceKind string
}

// NewFixtures returns the seeded assignment store.
func NewFixtures() *Fixtures {
	const tenant = "tenant-a"
	const kind = "patient_record"

	return &Fixtures{byKey: map[key]permissioncontext.Input{
		{tenant, DoctorWithRoleGrants, kind}: {
			Revision: SeededRevision,
			RolePermissions: []permissioncontext.RolePermission{
				{Role: "doctor", Action: "read", Enabled: true},
				{Role: "doctor", Action: "update", Enabled: true},
			},
		},
		{tenant, DoctorWithRevokedUpdate, kind}: {
			Revision: SeededRevision,
			RolePermissions: []permissioncontext.RolePermission{
				{Role: "doctor", Action: "read", Enabled: true},
				{Role: "doctor", Action: "update", Enabled: true},
			},
			UserOverrides: []permissioncontext.UserOverride{
				{Action: "update", State: permissioncontext.Revoke},
			},
		},
		{tenant, ClerkWithUserGrantOnly, kind}: {
			Revision: SeededRevision,
			RolePermissions: []permissioncontext.RolePermission{
				{Role: "clerk", Action: "delete", Enabled: false},
			},
			UserOverrides: []permissioncontext.UserOverride{
				{Action: "read", State: permissioncontext.Grant},
			},
		},
		{tenant, PrincipalWithNoAssignments, kind}: {
			Revision: SeededRevision,
		},
	}}
}

// For returns the assignments that apply, or an empty set when the principal has
// none. An absent principal is not an error: no assignments simply means Cerbos
// reaches default deny.
func (f *Fixtures) For(_ context.Context, query authz.AssignmentQuery) (permissioncontext.Input, error) {
	input, ok := f.byKey[key{
		tenantID:     query.TenantID,
		principalID:  query.PrincipalID,
		resourceKind: query.ResourceKind,
	}]
	if !ok {
		return permissioncontext.Input{Revision: SeededRevision}, nil
	}
	return input, nil
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
