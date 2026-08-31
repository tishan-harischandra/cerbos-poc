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

// tenantSaver is the one method Apply needs, so a test can satisfy it
// without standing up the entire assignmentstore.Store contract.
type tenantSaver interface {
	SaveTenant(ctx context.Context, tenant assignmentstore.Tenant) error
}

// Apply upserts every entry into the registry table. It is idempotent:
// running it again with an unchanged file leaves the same rows in place, so
// it is safe to run on every start rather than only on the first.
func Apply(ctx context.Context, store tenantSaver, entries []tenantregistry.Entry) error {
	for _, entry := range entries {
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
