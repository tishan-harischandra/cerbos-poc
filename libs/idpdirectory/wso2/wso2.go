// Package wso2 is the WSO2 Identity Server adapter (§7.4).
//
// It is a stub. Every operation reports idpdirectory.ErrUnimplemented with the
// SCIM2 API it would use, so an installation that selects WSO2_IS fails loudly
// and specifically rather than behaving as if the directory were empty.
//
// The stub is not scaffolding. It is the proof that the seam is real: it
// satisfies the same interface, is selected by the same configuration, and
// nothing in the ADS or the Admin Console had to change to make room for it.
package wso2

import (
	"context"
	"errors"
	"fmt"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// Config describes the WSO2 installation this adapter would serve (§7.4).
type Config struct {
	BaseURL string
	// TenantDomain is WSO2's tenant, mapped to an authorization tenant by
	// installation configuration.
	TenantDomain string
	TenantID     idpdirectory.TenantID
	// ServiceUser is the least-privileged machine identity. §7.4 forbids
	// proxying administrative credentials to the browser.
	ServiceUser  string
	ClientSecret string
}

// Directory is the WSO2 Identity Server adapter.
type Directory struct {
	cfg Config
}

// New returns the adapter.
func New(cfg Config) (*Directory, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("wso2: a base url is required")
	}
	if cfg.TenantID == "" {
		return nil, errors.New("wso2: a tenant is required")
	}
	return &Directory{cfg: cfg}, nil
}

func unimplemented(operation, api string) error {
	return fmt.Errorf("%w: %s would use the WSO2 %s API", idpdirectory.ErrUnimplemented, operation, api)
}

// SearchUsers is not implemented. It would use SCIM2 /Users.
func (d *Directory) SearchUsers(context.Context, idpdirectory.TenantID, idpdirectory.UserSearch) (idpdirectory.Page[idpdirectory.UserRef], error) {
	return idpdirectory.Page[idpdirectory.UserRef]{}, unimplemented("user search", "SCIM2 /Users")
}

// SearchRoles is not implemented. It would use SCIM2 /Roles.
func (d *Directory) SearchRoles(context.Context, idpdirectory.TenantID, idpdirectory.RoleSearch) (idpdirectory.Page[idpdirectory.RoleRef], error) {
	return idpdirectory.Page[idpdirectory.RoleRef]{}, unimplemented("role search", "SCIM2 /Roles")
}

// GetUser is not implemented. It would use SCIM2 /Users/{id}.
func (d *Directory) GetUser(context.Context, idpdirectory.TenantID, string) (idpdirectory.UserRef, error) {
	return idpdirectory.UserRef{}, unimplemented("user lookup", "SCIM2 /Users/{id}")
}

// GetRole is not implemented. It would use SCIM2 /Roles/{id}.
func (d *Directory) GetRole(context.Context, idpdirectory.TenantID, string) (idpdirectory.RoleRef, error) {
	return idpdirectory.RoleRef{}, unimplemented("role lookup", "SCIM2 /Roles/{id}")
}

// GetUserRoles is not implemented. It would use SCIM2 /Users/{id}?attributes=roles.
func (d *Directory) GetUserRoles(context.Context, idpdirectory.TenantID, string) ([]idpdirectory.RoleRef, error) {
	return nil, unimplemented("user role lookup", "SCIM2 /Users/{id}?attributes=roles")
}

// ResolveRuntimeRoles is not implemented. It would normalise WSO2 groups and
// roles into the same canonical identifiers §7.5 defines.
func (d *Directory) ResolveRuntimeRoles(context.Context, tokenverifier.VerifiedToken, idpdirectory.TenantID) ([]string, error) {
	return nil, unimplemented("runtime role resolution", "SCIM2 /Roles")
}

// OrganizationsOfTenant is not implemented (issue #85). WSO2 Identity
// Server's shape for this is organizations under SCIM2, which this stub
// does not model.
func (d *Directory) OrganizationsOfTenant(context.Context, idpdirectory.TenantID, idpdirectory.OrganizationSearch) (idpdirectory.Page[idpdirectory.OrganizationRef], error) {
	return idpdirectory.Page[idpdirectory.OrganizationRef]{}, unimplemented("organization search", "SCIM2 organizations")
}

// OrganizationsOfUser is not implemented. It would use SCIM2's own
// organization membership representation for a user.
func (d *Directory) OrganizationsOfUser(context.Context, idpdirectory.TenantID, string) ([]idpdirectory.OrganizationRef, error) {
	return nil, unimplemented("a user's organizations", "SCIM2 organizations")
}

// MembersOfOrganization is not implemented. It would use SCIM2's own
// organization membership representation.
func (d *Directory) MembersOfOrganization(context.Context, idpdirectory.TenantID, string, idpdirectory.PageRequest) (idpdirectory.Page[idpdirectory.UserRef], error) {
	return idpdirectory.Page[idpdirectory.UserRef]{}, unimplemented("organization members", "SCIM2 organizations")
}
