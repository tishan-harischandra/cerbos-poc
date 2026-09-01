package tenantregistry_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

func entries() []tenantregistry.Entry {
	return []tenantregistry.Entry{
		{Realm: "tenant-a", Issuer: "http://localhost:8081/realms/tenant-a", BrowserClientID: "patient-app"},
		{Realm: "tenant-b", Issuer: "http://localhost:8081/realms/tenant-b", BrowserClientID: "patient-app"},
	}
}

func TestTheHostsFirstLabelResolvesToItsTenant(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())

	entry, err := resolver.Resolve("tenant-a.example.test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if entry.Realm != "tenant-a" {
		t.Errorf("Realm = %q, want tenant-a", entry.Realm)
	}
	if entry.Issuer != "http://localhost:8081/realms/tenant-a" {
		t.Errorf("Issuer = %q, want the tenant's own issuer", entry.Issuer)
	}
}

func TestAPortIsStrippedBeforeResolution(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())

	entry, err := resolver.Resolve("tenant-b.example.test:4200")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if entry.Realm != "tenant-b" {
		t.Errorf("Realm = %q, want tenant-b", entry.Realm)
	}
}

func TestASecondTenantResolvesToItsOwnRealm(t *testing.T) {
	// The point of the whole exercise: two different hosts in the same
	// running stack resolve to two different tenants, not to whichever one
	// happened first.
	resolver := tenantregistry.NewHostResolver(entries())

	a, err := resolver.Resolve("tenant-a.example.test")
	if err != nil {
		t.Fatalf("Resolve(tenant-a): %v", err)
	}
	b, err := resolver.Resolve("tenant-b.example.test")
	if err != nil {
		t.Fatalf("Resolve(tenant-b): %v", err)
	}
	if a.Realm == b.Realm {
		t.Fatalf("two different hosts resolved to the same tenant (%q)", a.Realm)
	}
}

func TestAHostNamingNoRegisteredTenantIsRefused(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())

	if _, err := resolver.Resolve("tenant-nonexistent.example.test"); err == nil {
		t.Error("Resolve did not refuse an unregistered tenant")
	}
}

func TestAMissingHostIsRefused(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())

	if _, err := resolver.Resolve(""); err == nil {
		t.Error("Resolve did not refuse an empty host")
	}
}

func TestAHostWithNoSubdomainIsRefusedRatherThanGuessed(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())

	for _, host := range []string{"localhost", "localhost:8081", "example.test"} {
		t.Run(host, func(t *testing.T) {
			if _, err := resolver.Resolve(host); err == nil {
				t.Errorf("Resolve(%q) did not refuse a host with no tenant subdomain", host)
			}
		})
	}
}

func TestAnIPAddressHostIsRefusedRatherThanTakenAsATenantName(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())

	for _, host := range []string{"127.0.0.1", "127.0.0.1:8081", "[::1]:8081"} {
		t.Run(host, func(t *testing.T) {
			if _, err := resolver.Resolve(host); err == nil {
				t.Errorf("Resolve(%q) did not refuse an IP address", host)
			}
		})
	}
}

func TestAMalformedHostIsRefused(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())

	for _, host := range []string{".", "..example.test", ":8081"} {
		t.Run(host, func(t *testing.T) {
			if _, err := resolver.Resolve(host); err == nil {
				t.Errorf("Resolve(%q) did not refuse a malformed host", host)
			}
		})
	}
}

func TestResolveNeverPanicsOnAnyInput(t *testing.T) {
	// A defensive sweep, not a specific case: resolution sits in front of
	// every request this deployment serves, so nothing it can be handed
	// should ever crash the process.
	for _, host := range []string{"", ".", "...", ":", "::::", "a.b.c.d.e.f", "%zz", "tenant-a."} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Resolve(%q) panicked: %v", host, r)
				}
			}()
			_, _ = tenantregistry.NewHostResolver(entries()).Resolve(host)
		}()
	}
}

func TestNewHostResolverRejectsNothingUpFront(t *testing.T) {
	// An empty registry is a legitimate, if useless, configuration - every
	// host is then unknown, which is exactly what Resolve should report,
	// not a construction-time error.
	resolver := tenantregistry.NewHostResolver(nil)
	if _, err := resolver.Resolve("tenant-a.example.test"); err == nil {
		t.Fatal("an empty registry resolved a host anyway")
	}
}
