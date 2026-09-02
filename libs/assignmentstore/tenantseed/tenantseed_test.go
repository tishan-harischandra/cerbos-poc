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

func (f *fakeSaver) Tenant(_ context.Context, realm string) (assignmentstore.Tenant, bool, error) {
	tenant, ok := f.saved[realm]
	return tenant, ok, nil
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
}

// issue #86: once a realm has a row, the database is that row's value of
// record. Re-seeding the same file must not revert a change made after the
// first seed - whether that change came from the Admin Service's tenant
// onboarding endpoint or from an operator editing the row directly.
func TestApplyDoesNotRevertARealmAlreadyChangedInTheDatabase(t *testing.T) {
	saver := newFakeSaver()
	entries := []tenantregistry.Entry{
		{Realm: "tenant-a", Issuer: "http://localhost:8081/realms/tenant-a", BrowserClientID: "patient-app", ServiceClientID: "patient-app", CredentialSecretRef: "/secrets/tenant-a"},
	}

	if err := tenantseed.Apply(context.Background(), saver, entries); err != nil {
		t.Fatalf("Apply (first run): %v", err)
	}

	// An operator - or the tenant onboarding endpoint re-pointing a realm
	// at a new issuer - changes the row directly, outside this seeder.
	changed := saver.saved["tenant-a"]
	changed.Issuer = "https://new-issuer.example.test/realms/tenant-a"
	saver.saved["tenant-a"] = changed

	if err := tenantseed.Apply(context.Background(), saver, entries); err != nil {
		t.Fatalf("Apply (second run): %v", err)
	}

	if got := saver.saved["tenant-a"].Issuer; got != "https://new-issuer.example.test/realms/tenant-a" {
		t.Errorf("tenant-a.Issuer = %q after re-seeding, want the database value to survive", got)
	}
}

func TestApplySavesOnlyTheRealmsNotAlreadyPresent(t *testing.T) {
	saver := newFakeSaver()
	if err := saver.SaveTenant(context.Background(), assignmentstore.Tenant{
		Realm: "tenant-a", Issuer: "http://localhost:8081/realms/tenant-a",
		BrowserClientID: "patient-app", ServiceClientID: "patient-app", CredentialSecretRef: "/secrets/tenant-a",
	}); err != nil {
		t.Fatalf("pre-seeding tenant-a: %v", err)
	}
	saver.calls = 0

	entries := []tenantregistry.Entry{
		{Realm: "tenant-a", Issuer: "http://localhost:8081/realms/tenant-a", BrowserClientID: "patient-app", ServiceClientID: "patient-app", CredentialSecretRef: "/secrets/tenant-a"},
		{Realm: "tenant-b", Issuer: "http://localhost:8081/realms/tenant-b", BrowserClientID: "patient-app", ServiceClientID: "patient-app", CredentialSecretRef: "/secrets/tenant-b"},
	}
	if err := tenantseed.Apply(context.Background(), saver, entries); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if saver.calls != 1 {
		t.Errorf("SaveTenant was called %d times, want exactly 1 (tenant-b only)", saver.calls)
	}
	if _, ok := saver.saved["tenant-b"]; !ok {
		t.Error("tenant-b was not saved")
	}
}
