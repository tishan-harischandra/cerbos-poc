// Package tenantresolve resolves which tenants a service instance serves
// (issue #76, issue #77).
//
// #76 introduced the registry with exactly one row; #77 lets a deployment
// serve every realm the registry names. Single keeps the one-tenant
// contract for anything that still wants it; All waits for the registry to
// be seeded at all and returns every row, so a service can build one
// installation per realm.
package tenantresolve

import (
	"context"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Lister is the one method Single needs, so a test can satisfy it without
// standing up the entire assignmentstore.Store contract.
type Lister interface {
	Tenants(ctx context.Context) ([]assignmentstore.Tenant, error)
}

// PollInterval is how often Single retries while the registry is not yet
// seeded. A variable, rather than a constant, so a test can shrink it
// instead of waiting out the real interval.
var PollInterval = 2 * time.Second

// Single returns the one row in the tenant registry, retrying on ctx until
// it succeeds or ctx is done.
//
// `make up` starts every service container before running migrate, seed
// and seed-tenants (the same order the existing role-matrix seed already
// tolerates), so a service that resolved the registry once at startup and
// gave up would crash-loop on every fresh volume rather than simply
// waiting the few seconds seed-tenants takes.
func Single(ctx context.Context, tenants Lister) (assignmentstore.Tenant, error) {
	var lastErr error
	for {
		all, err := tenants.Tenants(ctx)
		switch {
		case err != nil:
			lastErr = fmt.Errorf("tenantresolve: listing the tenant registry: %w", err)
		case len(all) == 1:
			return all[0], nil
		default:
			lastErr = fmt.Errorf("tenantresolve: the tenant registry has %d rows, want exactly one; has the tenant registry seed step run?", len(all))
		}

		select {
		case <-ctx.Done():
			return assignmentstore.Tenant{}, lastErr
		case <-time.After(PollInterval):
		}
	}
}

// All returns every row in the tenant registry, retrying on ctx until at
// least one exists or ctx is done (issue #77). A deployment that serves more
// than one realm builds one installation per row this returns, rather than
// refusing to start until the registry holds exactly one.
func All(ctx context.Context, tenants Lister) ([]assignmentstore.Tenant, error) {
	var lastErr error
	for {
		all, err := tenants.Tenants(ctx)
		switch {
		case err != nil:
			lastErr = fmt.Errorf("tenantresolve: listing the tenant registry: %w", err)
		case len(all) > 0:
			return all, nil
		default:
			lastErr = fmt.Errorf("tenantresolve: the tenant registry has no rows; has the tenant registry seed step run?")
		}

		select {
		case <-ctx.Done():
			return nil, lastErr
		case <-time.After(PollInterval):
		}
	}
}
