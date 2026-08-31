package tenantresolve_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/tenantresolve"
)

// fakeLister lets a test change what Tenants returns between calls, so
// Single's retry loop can be observed reading a registry that starts empty
// and is seeded partway through.
type fakeLister struct {
	mu      sync.Mutex
	tenants []assignmentstore.Tenant
}

func (f *fakeLister) Tenants(context.Context) ([]assignmentstore.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tenants, nil
}

func (f *fakeLister) set(tenants []assignmentstore.Tenant) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants = tenants
}

func TestSingleReturnsTheOnlyTenant(t *testing.T) {
	want := assignmentstore.Tenant{Realm: "tenant-a"}
	got, err := tenantresolve.Single(context.Background(), &fakeLister{tenants: []assignmentstore.Tenant{want}})
	if err != nil {
		t.Fatalf("Single: %v", err)
	}
	if got != want {
		t.Errorf("Single = %+v, want %+v", got, want)
	}
}

// `make up` starts every service container before running migrate, seed and
// seed-tenants, so Single must keep retrying rather than fail the instant it
// finds an empty registry.
func TestSingleRetriesUntilTheRegistryIsSeeded(t *testing.T) {
	original := tenantresolve.PollInterval
	tenantresolve.PollInterval = 5 * time.Millisecond
	defer func() { tenantresolve.PollInterval = original }()

	lister := &fakeLister{}
	want := assignmentstore.Tenant{Realm: "tenant-a"}
	go func() {
		time.Sleep(20 * time.Millisecond)
		lister.set([]assignmentstore.Tenant{want})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := tenantresolve.Single(ctx, lister)
	if err != nil {
		t.Fatalf("Single: %v", err)
	}
	if got != want {
		t.Errorf("Single = %+v, want %+v", got, want)
	}
}

func TestSingleRejectsZeroTenantsOnceContextExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := tenantresolve.Single(ctx, &fakeLister{})
	if err == nil {
		t.Fatal("Single accepted an empty tenant registry")
	}
}

func TestSingleRejectsMoreThanOneTenantOnceContextExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := tenantresolve.Single(ctx, &fakeLister{tenants: []assignmentstore.Tenant{
		{Realm: "tenant-a"}, {Realm: "tenant-b"},
	}})
	if err == nil {
		t.Fatal("Single accepted a registry with more than one tenant")
	}
}

// A deployment that serves more than one realm (issue #77) reads every row,
// rather than refusing to start the way Single does.
func TestAllReturnsEveryTenant(t *testing.T) {
	want := []assignmentstore.Tenant{{Realm: "tenant-a"}, {Realm: "tenant-b"}}
	got, err := tenantresolve.All(context.Background(), &fakeLister{tenants: want})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("All = %+v, want %+v", got, want)
	}
}

func TestAllRetriesUntilTheRegistryIsSeeded(t *testing.T) {
	original := tenantresolve.PollInterval
	tenantresolve.PollInterval = 5 * time.Millisecond
	defer func() { tenantresolve.PollInterval = original }()

	lister := &fakeLister{}
	want := []assignmentstore.Tenant{{Realm: "tenant-a"}, {Realm: "tenant-b"}}
	go func() {
		time.Sleep(20 * time.Millisecond)
		lister.set(want)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := tenantresolve.All(ctx, lister)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("All = %+v, want %+v", got, want)
	}
}

func TestAllRejectsZeroTenantsOnceContextExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := tenantresolve.All(ctx, &fakeLister{})
	if err == nil {
		t.Fatal("All accepted an empty tenant registry")
	}
}
