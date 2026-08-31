package tenantregistry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

// writeSecret writes a readable secret file and returns its path.
func writeSecret(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("shh"), 0o600); err != nil {
		t.Fatalf("writing secret file: %v", err)
	}
	return path
}

func TestAValidFileParsesToItsEntries(t *testing.T) {
	secret := writeSecret(t)
	raw := `
- realm: tenant-a
  issuer: http://localhost:8081/realms/tenant-a
  browserClientId: patient-app
  serviceClientId: authorization-admin-service
  credentialSecretRef: ` + secret + `
`
	entries, err := tenantregistry.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	got := entries[0]
	want := tenantregistry.Entry{
		Realm:               "tenant-a",
		Issuer:              "http://localhost:8081/realms/tenant-a",
		BrowserClientID:     "patient-app",
		ServiceClientID:     "authorization-admin-service",
		CredentialSecretRef: secret,
	}
	if got != want {
		t.Errorf("entries[0] = %+v, want %+v", got, want)
	}
}

// §7.1 no longer distinguishes a browser client from a service client when
// an installation has only the one: the service client defaults to the
// browser client rather than being left empty.
func TestTheServiceClientDefaultsToTheBrowserClient(t *testing.T) {
	secret := writeSecret(t)
	raw := `
- realm: tenant-a
  issuer: http://localhost:8081/realms/tenant-a
  browserClientId: patient-app
  credentialSecretRef: ` + secret + `
`
	entries, err := tenantregistry.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if entries[0].ServiceClientID != "patient-app" {
		t.Errorf("ServiceClientID = %q, want it to default to the browser client", entries[0].ServiceClientID)
	}
}

func TestADuplicateRealmIsRefusedWithAClearError(t *testing.T) {
	secret := writeSecret(t)
	raw := `
- realm: tenant-a
  issuer: http://localhost:8081/realms/tenant-a
  browserClientId: patient-app
  credentialSecretRef: ` + secret + `
- realm: tenant-a
  issuer: http://localhost:8082/realms/tenant-a
  browserClientId: other-app
  credentialSecretRef: ` + secret + `
`
	_, err := tenantregistry.Parse([]byte(raw))
	if err == nil {
		t.Fatal("Parse accepted a registry file declaring the same realm twice")
	}
	if !strings.Contains(err.Error(), "tenant-a") {
		t.Errorf("error = %v, want it to name the duplicated realm", err)
	}
}

func TestAMissingIssuerIsRefusedWithAClearError(t *testing.T) {
	secret := writeSecret(t)
	raw := `
- realm: tenant-a
  browserClientId: patient-app
  credentialSecretRef: ` + secret + `
`
	_, err := tenantregistry.Parse([]byte(raw))
	if err == nil {
		t.Fatal("Parse accepted a registry entry with no issuer")
	}
	if !strings.Contains(err.Error(), "issuer") || !strings.Contains(err.Error(), "tenant-a") {
		t.Errorf("error = %v, want it to name the realm and mention the missing issuer", err)
	}
}

func TestAnUnreadableSecretReferenceIsRefusedWithAClearError(t *testing.T) {
	raw := `
- realm: tenant-a
  issuer: http://localhost:8081/realms/tenant-a
  browserClientId: patient-app
  credentialSecretRef: /nonexistent/path/to/secret
`
	_, err := tenantregistry.Parse([]byte(raw))
	if err == nil {
		t.Fatal("Parse accepted a registry entry whose secret reference cannot be read")
	}
	if !strings.Contains(err.Error(), "tenant-a") {
		t.Errorf("error = %v, want it to name the realm whose secret reference failed", err)
	}
}

// Parsing is pure: it must never dial the network to validate an entry, so
// an issuer or base URL that resolves to nothing is still a successful
// parse. Whether the issuer is reachable is a runtime concern for whatever
// verifies tokens against it, not for this file's syntax and shape.
func TestParsingNeverDialsTheNetwork(t *testing.T) {
	secret := writeSecret(t)
	raw := `
- realm: tenant-a
  issuer: http://this-host-does-not-resolve.invalid/realms/tenant-a
  browserClientId: patient-app
  credentialSecretRef: ` + secret + `
`
	if _, err := tenantregistry.Parse([]byte(raw)); err != nil {
		t.Fatalf("Parse: %v, want a syntactically valid entry to parse regardless of whether its issuer resolves", err)
	}
}

func TestParseFileReadsFromDisk(t *testing.T) {
	secret := writeSecret(t)
	path := filepath.Join(t.TempDir(), "tenants.yaml")
	contents := `
- realm: tenant-a
  issuer: http://localhost:8081/realms/tenant-a
  browserClientId: patient-app
  credentialSecretRef: ` + secret + `
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing registry file: %v", err)
	}

	entries, err := tenantregistry.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(entries) != 1 || entries[0].Realm != "tenant-a" {
		t.Errorf("entries = %+v, want one entry for tenant-a", entries)
	}
}

func TestAMissingFileIsRefused(t *testing.T) {
	_, err := tenantregistry.ParseFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("ParseFile accepted a path that does not exist")
	}
}
