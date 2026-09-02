// Package tenantseed writes the tenant registry file's entries into the
// database of record (issue #76).
//
// The file is not the database of record itself - it is what re-seeds the
// database on every start, so the registry table (and therefore the set of
// trusted realms) is reproducible from version control rather than living
// only in whatever an administrator once typed into a running database.
package tenantseed

import (
	"context"
	"fmt"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

// tenantSaver is the slice of Apply's collaborator it needs, so a test can
// satisfy it without standing up the entire assignmentstore.Store contract.
type tenantSaver interface {
	Tenant(ctx context.Context, realm string) (assignmentstore.Tenant, bool, error)
	SaveTenant(ctx context.Context, tenant assignmentstore.Tenant) error
}

// Apply inserts every entry the registry table does not already have a row
// for. It is idempotent: running it again with an unchanged file leaves the
// same rows in place, so it is safe to run on every start rather than only
// on the first.
//
// It is also, deliberately, not an upsert (issue #86): once a realm has a
// row - whether tenantseed put it there or the Admin Service's tenant
// onboarding endpoint did - the database is that row's value of record.
// Re-running this on a later `make up` with the same file must not silently
// revert a change an operator made at runtime back to whatever the file
// still says.
func Apply(ctx context.Context, store tenantSaver, entries []tenantregistry.Entry) error {
	for _, entry := range entries {
		_, exists, err := store.Tenant(ctx, entry.Realm)
		if err != nil {
			return fmt.Errorf("tenantseed: reading realm %q: %w", entry.Realm, err)
		}
		if exists {
			continue
		}

		tenant := assignmentstore.Tenant{
			Realm:               entry.Realm,
			Issuer:              entry.Issuer,
			BrowserClientID:     entry.BrowserClientID,
			ServiceClientID:     entry.ServiceClientID,
			CredentialSecretRef: entry.CredentialSecretRef,
		}
		if err := store.SaveTenant(ctx, tenant); err != nil {
			return fmt.Errorf("tenantseed: saving realm %q: %w", entry.Realm, err)
		}
	}
	return nil
}
