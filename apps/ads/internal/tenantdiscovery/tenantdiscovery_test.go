package tenantdiscovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tenantdiscovery"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

type fakeLister struct {
	tenants []assignmentstore.Tenant
	err     error
}

func (f *fakeLister) Tenants(context.Context) ([]assignmentstore.Tenant, error) {
	return f.tenants, f.err
}

// A tenant already known at startup (issue #77's normal wiring) costs a
// pass nothing: OnNewTenant is never called for it.
func TestDiscoverOnceLeavesAnAlreadyKnownTenantAlone(t *testing.T) {
	lister := &fakeLister{tenants: []assignmentstore.Tenant{{Realm: "tenant-a"}}}
	var onboarded []string
	discoverer := tenantdiscovery.New(tenantdiscovery.Config{
		Store: lister,
		Known: []string{"tenant-a"},
		OnNewTenant: func(_ context.Context, tenant assignmentstore.Tenant) error {
			onboarded = append(onboarded, tenant.Realm)
			return nil
		},
	})

	discoverer.DiscoverOnce(context.Background())

	if len(onboarded) != 0 {
		t.Errorf("OnNewTenant was called for an already-known tenant: %v", onboarded)
	}
}

// issue #86's own acceptance criterion: a tenant onboarded at runtime
// becomes usable with no restart - discovering it is the mechanism.
func TestDiscoverOnceCallsOnNewTenantForARealmNotYetKnown(t *testing.T) {
	lister := &fakeLister{tenants: []assignmentstore.Tenant{{Realm: "tenant-a"}, {Realm: "tenant-c"}}}
	var onboarded []string
	discoverer := tenantdiscovery.New(tenantdiscovery.Config{
		Store: lister,
		Known: []string{"tenant-a"},
		OnNewTenant: func(_ context.Context, tenant assignmentstore.Tenant) error {
			onboarded = append(onboarded, tenant.Realm)
			return nil
		},
	})

	discoverer.DiscoverOnce(context.Background())

	if len(onboarded) != 1 || onboarded[0] != "tenant-c" {
		t.Errorf("onboarded = %v, want exactly [tenant-c]", onboarded)
	}
}

// Discovering the same new tenant twice in a row - two passes finding it
// still there - must call OnNewTenant only once: the second pass has
// already marked it known.
func TestANewTenantIsOnboardedOnlyOnce(t *testing.T) {
	lister := &fakeLister{tenants: []assignmentstore.Tenant{{Realm: "tenant-c"}}}
	calls := 0
	discoverer := tenantdiscovery.New(tenantdiscovery.Config{
		Store: lister,
		OnNewTenant: func(context.Context, assignmentstore.Tenant) error {
			calls++
			return nil
		},
	})

	discoverer.DiscoverOnce(context.Background())
	discoverer.DiscoverOnce(context.Background())

	if calls != 1 {
		t.Errorf("OnNewTenant was called %d times, want exactly 1", calls)
	}
}

// A tenant OnNewTenant fails for must be retried on the next pass, not
// left permanently unusable because one pass hit a transient error.
func TestATenantThatFailsToOnboardIsRetriedOnTheNextPass(t *testing.T) {
	lister := &fakeLister{tenants: []assignmentstore.Tenant{{Realm: "tenant-c"}}}
	calls := 0
	var errs []error
	discoverer := tenantdiscovery.New(tenantdiscovery.Config{
		Store: lister,
		OnNewTenant: func(context.Context, assignmentstore.Tenant) error {
			calls++
			if calls == 1 {
				return errors.New("the identity provider configuration could not be read")
			}
			return nil
		},
		OnError: func(err error) { errs = append(errs, err) },
	})

	discoverer.DiscoverOnce(context.Background())
	discoverer.DiscoverOnce(context.Background())

	if calls != 2 {
		t.Errorf("OnNewTenant was called %d times, want exactly 2 (retried once)", calls)
	}
	if len(errs) != 1 {
		t.Errorf("OnError was called %d times, want exactly 1", len(errs))
	}
}

// A registry read failure must not stall discovery of tenants found on a
// later, successful pass.
func TestAFailedListDoesNotStopDiscoveryPermanently(t *testing.T) {
	lister := &fakeLister{err: errors.New("the authorization database is not reachable")}
	var errs []error
	var onboarded []string
	discoverer := tenantdiscovery.New(tenantdiscovery.Config{
		Store: lister,
		OnNewTenant: func(_ context.Context, tenant assignmentstore.Tenant) error {
			onboarded = append(onboarded, tenant.Realm)
			return nil
		},
		OnError: func(err error) { errs = append(errs, err) },
	})

	discoverer.DiscoverOnce(context.Background())
	if len(errs) != 1 {
		t.Fatalf("OnError was called %d times on the failing pass, want exactly 1", len(errs))
	}

	lister.err = nil
	lister.tenants = []assignmentstore.Tenant{{Realm: "tenant-c"}}
	discoverer.DiscoverOnce(context.Background())

	if len(onboarded) != 1 || onboarded[0] != "tenant-c" {
		t.Errorf("onboarded = %v, want exactly [tenant-c] once the registry was reachable again", onboarded)
	}
}

// Run ticks on its own until ctx is done, discovering a tenant that
// appears between ticks with no restart.
func TestRunDiscoversATenantThatAppearsBetweenTicks(t *testing.T) {
	lister := &fakeLister{tenants: []assignmentstore.Tenant{{Realm: "tenant-a"}}}
	onboarded := make(chan string, 1)
	discoverer := tenantdiscovery.New(tenantdiscovery.Config{
		Store:    lister,
		Known:    []string{"tenant-a"},
		Interval: 5 * time.Millisecond,
		OnNewTenant: func(_ context.Context, tenant assignmentstore.Tenant) error {
			onboarded <- tenant.Realm
			return nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go discoverer.Run(ctx)

	time.Sleep(20 * time.Millisecond)
	lister.tenants = append(lister.tenants, assignmentstore.Tenant{Realm: "tenant-c"})

	select {
	case realm := <-onboarded:
		if realm != "tenant-c" {
			t.Errorf("onboarded realm = %q, want tenant-c", realm)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run never discovered the newly onboarded tenant")
	}
}
