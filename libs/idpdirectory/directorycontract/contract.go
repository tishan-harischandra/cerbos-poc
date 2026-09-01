// Package directorycontract is the suite every identity directory adapter's
// organization reads must pass, unchanged, whatever provider is behind them
// (issue #85).
//
// The assertions here never depend on adapter-specific fixture data for the
// one guarantee that has to hold identically everywhere: a lookup naming a
// tenant the adapter does not serve is an error, never a filtered or empty
// result. A leaked service-account credential is scoped to one tenant only
// if every adapter refuses to answer for another one.
package directorycontract

import (
	"context"
	"errors"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
)

// Factory builds the directory under test, and OtherTenant is a tenant the
// built directory does not serve - the cross-tenant case every case below
// exercises.
type Factory func(t *testing.T) (directory idpdirectory.IdentityDirectory, otherTenant idpdirectory.TenantID)

// Run executes the whole contract against one adapter.
func Run(t *testing.T, newDirectory Factory) {
	t.Helper()

	t.Run("organizations of another tenant is refused, not filtered or empty", func(t *testing.T) {
		directory, otherTenant := newDirectory(t)
		_, err := directory.OrganizationsOfTenant(context.Background(), otherTenant, idpdirectory.OrganizationSearch{})
		assertUnknownTenant(t, "OrganizationsOfTenant", err)
	})

	t.Run("a user's organizations in another tenant is refused, not filtered or empty", func(t *testing.T) {
		directory, otherTenant := newDirectory(t)
		_, err := directory.OrganizationsOfUser(context.Background(), otherTenant, "a-user")
		assertUnknownTenant(t, "OrganizationsOfUser", err)
	})

	t.Run("members of an organization in another tenant is refused, not filtered or empty", func(t *testing.T) {
		directory, otherTenant := newDirectory(t)
		_, err := directory.MembersOfOrganization(context.Background(), otherTenant, "an-organization", idpdirectory.PageRequest{})
		assertUnknownTenant(t, "MembersOfOrganization", err)
	})
}

// assertUnknownTenant requires an error - ErrUnknownTenant for an adapter
// that actually serves organizations, or any other error (ErrUnimplemented,
// for a stub that serves none of them) for one that does not. Either way,
// nothing about another tenant's organizations is ever returned as data.
func assertUnknownTenant(t *testing.T, operation string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s for another tenant returned no error; a cross-tenant lookup must never succeed", operation)
	}
	if errors.Is(err, idpdirectory.ErrUnimplemented) {
		return
	}
	if !errors.Is(err, idpdirectory.ErrUnknownTenant) {
		t.Errorf("%s error = %v, want %v (or %v for a stub)", operation, err, idpdirectory.ErrUnknownTenant, idpdirectory.ErrUnimplemented)
	}
}
