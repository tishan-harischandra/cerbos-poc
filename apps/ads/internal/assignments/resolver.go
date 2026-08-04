package assignments

import (
	"context"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// RoleMatrix is the slice of the authorization database the decision path
// reads. It is declared here, at the consumer, rather than being the whole
// store: the ADS has no business writing the matrix, and a narrow port is what
// makes the read path testable without a database.
type RoleMatrix interface {
	ActiveRolePermissions(ctx context.Context, query assignmentstore.ActiveRolePermissionQuery) ([]assignmentstore.RolePermission, error)
	PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error)
}

// Overrides supplies the user-level overrides that apply to one principal and
// one resource.
//
// It is a separate collaborator from the role matrix because the two are
// resolved from different places and change at different times. The seeded
// implementation below is what the user override slice replaces with a
// database-backed one.
type Overrides interface {
	For(ctx context.Context, query authz.AssignmentQuery) ([]permissioncontext.UserOverride, error)
}

// ResolverConfig holds the resolver's collaborators.
type ResolverConfig struct {
	Matrix    RoleMatrix
	Overrides Overrides
	// Now is the clock the validity windows are judged against. Injected so a
	// decision, a test and a replay can all agree on when "now" was.
	Now func() time.Time
}

// Resolver answers "what applies to this principal and this resource" from the
// authorization database.
//
// Everything it returns is a fact: which actions the principal's roles grant,
// which the user override grants or revokes, and the revision they were read
// at. It never reconciles them. A revoke that cancels a role grant is
// precedence, precedence lives in Cerbos policy (§6.3, ADR-003), and deciding
// it here would be the duplicated-logic failure mode §21 warns about.
type Resolver struct {
	matrix    RoleMatrix
	overrides Overrides
	now       func() time.Time
}

// NewResolver builds a resolver over the authorization database.
func NewResolver(cfg ResolverConfig) *Resolver {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Resolver{matrix: cfg.Matrix, overrides: cfg.Overrides, now: now}
}

// For resolves the permissions that apply to one principal and one resource.
func (r *Resolver) For(ctx context.Context, query authz.AssignmentQuery) (permissioncontext.Input, error) {
	at := r.now()

	// One query for every role the principal holds, not one per role: a
	// principal carries dozens of roles and this is the hot path (§11.2).
	permissions, err := r.matrix.ActiveRolePermissions(ctx, assignmentstore.ActiveRolePermissionQuery{
		TenantID:        query.TenantID,
		RoleExternalIDs: canonicalRoles(query.IdPRoles),
		ResourceKey:     query.ResourceKind,
		At:              at,
	})
	if err != nil {
		return permissioncontext.Input{}, fmt.Errorf("reading the role matrix: %w", err)
	}

	// A tenant whose matrix has never been saved has no revision yet. That is
	// an absence, not a failure, and it reads as revision zero.
	revision, found, err := r.matrix.PermissionRevision(ctx, query.TenantID)
	if err != nil {
		return permissioncontext.Input{}, fmt.Errorf("reading the permission revision: %w", err)
	}
	input := permissioncontext.Input{}
	if found {
		input.Revision = revision.Revision
	}

	for _, permission := range permissions {
		input.RolePermissions = append(input.RolePermissions, permissioncontext.RolePermission{
			Role:    permission.Key.RoleExternalID,
			Action:  permission.Key.ActionKey,
			Enabled: permission.Enabled,
		})
	}

	if r.overrides != nil {
		overrides, err := r.overrides.For(ctx, query)
		if err != nil {
			return permissioncontext.Input{}, fmt.Errorf("reading the user overrides: %w", err)
		}
		input.UserOverrides = overrides
	}

	return input, nil
}

// canonicalRoles drops empty entries so a stray blank in a token's role claim
// cannot become a lookup for the empty role.
//
// The identifiers are otherwise passed through untouched: they are already the
// §7.5 canonical form the matrix is keyed by, and normalising a token claim
// into that form is the identity adapter's job, not this one's.
func canonicalRoles(roles []string) []string {
	canonical := make([]string, 0, len(roles))
	for _, role := range roles {
		if role != "" {
			canonical = append(canonical, role)
		}
	}
	if len(canonical) == 0 {
		return nil
	}
	return canonical
}
