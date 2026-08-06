// Package idpdirectory is the provider-neutral identity directory port (§7.2).
//
// It is a library rather than a service (ADR: the IdP adapter is a shared
// library behind dependency inversion). Consumers depend on this package only;
// the concrete Keycloak and WSO2 adapters live below it and are reachable only
// through the provider factory, so swapping providers is a configuration change
// rather than a code change. An architecture test enforces that boundary.
package idpdirectory

import (
	"context"
	"errors"

	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// The failures every adapter reports the same way, so a consumer can react to
// them without knowing which provider is installed.
var (
	// ErrUnimplemented means the installed provider does not offer the
	// operation. The WSO2 stub returns it for everything.
	ErrUnimplemented = errors.New("the identity provider adapter does not implement this operation")
	// ErrUnknownTenant means the adapter does not serve the tenant asked
	// about. An adapter is configured for one realm or organisation, so a
	// query for another tenant is a bug, not an empty result.
	ErrUnknownTenant = errors.New("the identity provider adapter does not serve this tenant")
	// ErrNotFound means the user or role does not exist.
	ErrNotFound = errors.New("no such identity")
)

// TenantID is the authorization tenant an identity belongs to.
type TenantID string

// UserRef is a user as the directory knows it. ExternalID is the stable
// identifier the authorization database persists (§7.3); everything else is
// display metadata that may change.
type UserRef struct {
	ExternalID  string
	Username    string
	DisplayName string
	Email       string
	Enabled     bool
}

// RoleRef is a role as the directory knows it. CanonicalID is the §7.5
// identifier the role-permission matrix is keyed by, and it is byte-identical
// to the one token normalisation produces for the same role.
type RoleRef struct {
	CanonicalID string
	ExternalID  string
	Name        string
	Description string
}

// PageRequest is one window over a result set. Both fields are honoured by
// every adapter: the Admin Console pages through hundreds of thousands of
// users, so an adapter that quietly returned everything would be unusable.
type PageRequest struct {
	Offset int
	Limit  int
}

// DefaultPageLimit is used when a caller asks for no particular size.
const DefaultPageLimit = 50

// MaxPageLimit caps what a caller may ask for, so one request cannot pull the
// whole directory into memory.
const MaxPageLimit = 500

// Normalised clamps a page request into the supported range.
func (p PageRequest) Normalised() PageRequest {
	normalised := p
	if normalised.Offset < 0 {
		normalised.Offset = 0
	}
	if normalised.Limit <= 0 {
		normalised.Limit = DefaultPageLimit
	}
	if normalised.Limit > MaxPageLimit {
		normalised.Limit = MaxPageLimit
	}
	return normalised
}

// UserSearch selects users. Query is a free-text match on username, name or
// email, as the provider defines it.
type UserSearch struct {
	Query string
	Page  PageRequest
}

// RoleSearch selects roles.
type RoleSearch struct {
	Query string
	Page  PageRequest
}

// Page is one window of results plus what a caller needs to ask for the next.
type Page[T any] struct {
	Items   []T
	Offset  int
	Limit   int
	HasMore bool
}

// IdentityDirectory is the §7.2 adapter interface.
type IdentityDirectory interface {
	SearchUsers(ctx context.Context, tenant TenantID, query UserSearch) (Page[UserRef], error)
	SearchRoles(ctx context.Context, tenant TenantID, query RoleSearch) (Page[RoleRef], error)
	GetUser(ctx context.Context, tenant TenantID, externalID string) (UserRef, error)
	GetRole(ctx context.Context, tenant TenantID, externalID string) (RoleRef, error)
	// GetUserRoles reports the canonical roles directly assigned to one
	// user, from the same authoritative role source SearchRoles reads
	// (§7.3). It exists for the Admin Console's user-override screen
	// (issue #17): the "underlying role result" preview it shows before
	// saving is computed from these roles, the same way SaveRoleMatrix's
	// caller supplies a role's own roleExternalIds - a directory read, not
	// a second implementation of anything the ADS decides.
	GetUserRoles(ctx context.Context, tenant TenantID, userExternalID string) ([]RoleRef, error)
	// ResolveRuntimeRoles reports the canonical roles a verified token grants
	// within a tenant. It reads the token rather than the directory: a
	// directory round trip on the decision path would put the identity
	// provider's availability in front of every authorization call.
	ResolveRuntimeRoles(ctx context.Context, token tokenverifier.VerifiedToken, tenant TenantID) ([]string, error)
}
