// Package tenantdiscovery notices a tenant onboarded at runtime (issue
// #86) and builds the same Installation the ADS built for every realm at
// startup, with no service restart.
//
// It reuses the reconciler's own periodic-poll shape (§10.3) rather than
// adding a second invalidation mechanism: the tenant registry table is
// polled on an interval, exactly the way invalidation.Reconciler already
// polls PermissionRevision, and a realm this replica has never seen before
// is the one thing a poll can find that a Kafka event never named.
package tenantdiscovery

import (
	"context"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// DefaultInterval is how often Discoverer.Run checks for a tenant this
// replica has not registered yet, when Config.Interval is left zero.
const DefaultInterval = 5 * time.Second

// Lister is the one method Discoverer needs from the tenant registry, so a
// test can satisfy it without standing up the entire assignmentstore.Store
// contract.
type Lister interface {
	Tenants(ctx context.Context) ([]assignmentstore.Tenant, error)
}

// Config holds Discoverer's collaborators.
type Config struct {
	Store Lister
	// Known are the realms already registered before Discoverer starts -
	// the ones this replica built an Installation for at startup. Seeding
	// them here means the first poll costs nothing for a deployment that
	// onboards no new tenant.
	Known []string
	// OnNewTenant is called once for each realm this replica has not seen
	// before, in the order Store.Tenants returns them. A failure is
	// reported through OnError and that realm is retried on the next
	// poll - the same way a Kafka message this package never receives at
	// all changes nothing about eventual correctness (§10.3).
	OnNewTenant func(ctx context.Context, tenant assignmentstore.Tenant) error
	// Interval bounds how often a poll runs. Zero means DefaultInterval.
	Interval time.Duration
	// OnError, if set, is called with any error listing the registry or
	// building one tenant's installation. A poll that fails on one tenant
	// still tries every other tenant it found.
	OnError func(error)
}

// Discoverer is the poll loop.
type Discoverer struct {
	cfg   Config
	known map[string]bool
}

// New applies Config's defaults and returns a Discoverer.
func New(cfg Config) *Discoverer {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	known := make(map[string]bool, len(cfg.Known))
	for _, realm := range cfg.Known {
		known[realm] = true
	}
	return &Discoverer{cfg: cfg, known: known}
}

// DiscoverOnce runs one pass: listing the registry and calling OnNewTenant
// for every realm not already known. A realm OnNewTenant fails for is not
// marked known, so the next pass retries it.
func (d *Discoverer) DiscoverOnce(ctx context.Context) {
	tenants, err := d.cfg.Store.Tenants(ctx)
	if err != nil {
		d.reportError(fmt.Errorf("tenantdiscovery: listing the tenant registry: %w", err))
		return
	}

	for _, tenant := range tenants {
		if d.known[tenant.Realm] {
			continue
		}
		if err := d.cfg.OnNewTenant(ctx, tenant); err != nil {
			d.reportError(fmt.Errorf("tenantdiscovery: onboarding realm %q: %w", tenant.Realm, err))
			continue
		}
		d.known[tenant.Realm] = true
	}
}

func (d *Discoverer) reportError(err error) {
	if d.cfg.OnError != nil {
		d.cfg.OnError(err)
	}
}

// Run calls DiscoverOnce immediately, then on every tick of Interval,
// until ctx is done.
func (d *Discoverer) Run(ctx context.Context) {
	d.DiscoverOnce(ctx)

	ticker := time.NewTicker(d.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.DiscoverOnce(ctx)
		}
	}
}
