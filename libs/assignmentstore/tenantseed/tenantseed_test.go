package tenantseed_test

import (
	"context"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/tenantseed"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

// fakeSaver records every tenant it was asked to save, keyed by realm, so a
// test can assert both what was saved and that seeding twice does not grow
// the recorded set past one entry per realm.
type fakeSaver struct {
	saved map[string]assignmentstore.Tenant
	calls int
}

func newFakeSaver() *fakeSaver {
	return &fakeSaver{saved: make(map[string]assignmentstore.Tenant)}
}

func (f *fakeSaver) SaveTenant(_ context.Context, tenant assignmentstore.Tenant) error {
	f.calls++
	f.saved[tenant.Realm] = tenant
	return nil
}

func TestApplySavesEveryEntry(t *testing.T) {
	saver := newFakeSaver()
	entries := []tenantregistry.Entry{
		{Realm: "tenant-a", Issuer: "http://localhost:8081/realms/tenant-a", BrowserClientID: "patient-app", ServiceClientID: "authorization-admin-service", CredentialSecretRef: "/secrets/tenant-a"},
		{Realm: "tenant-b", Issuer: "http://localhost:8081/realms/tenant-b", BrowserClientID: "patient-app", ServiceClientID: "authorization-admin-service", CredentialSecretRef: "/secrets/tenant-b"},
	}

	if err := tenantseed.Apply(context.Background(), saver, entries); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(saver.saved) != 2 {
		t.Fatalf("saved = %+v, want two realms", saver.saved)
	}
	if saver.saved["tenant-a"].Issuer != "http://localhost:8081/realms/tenant-a" {
		t.Errorf("tenant-a.Issuer = %q, want it to carry the parsed entry's issuer", saver.saved["tenant-a"].Issuer)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	saver := newFakeSaver()
	entries := []tenantregistry.Entry{
		{Realm: "tenant-a", Issuer: "http://localhost:8081/realms/tenant-a", BrowserClientID: "patient-app", ServiceClientID: "patient-app", CredentialSecretRef: "/secrets/tenant-a"},
	}

	if err := tenantseed.Apply(context.Background(), saver, entries); err != nil {
		t.Fatalf("Apply (first run): %v", err)
	}
	if err := tenantseed.Apply(context.Background(), saver, entries); err != nil {
		t.Fatalf("Apply (second run): %v", err)
	}

	if len(saver.saved) != 1 {
		t.Errorf("saved = %+v, want exactly one realm after two runs of the same file", saver.saved)
	}
	if saver.calls != 2 {
		t.Errorf("calls = %d, want SaveTenant called once per run (2), the store itself owning idempotency", saver.calls)
	}
}
