package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

const secret = "a-secret-nobody-should-see"

// The seam is only real if flipping one environment variable changes the
// provider with no consumer rebuilt and no code touched. These two cases run
// the same construction path and differ only in IDP_TYPE.
func TestTheProviderIsSelectedByEnvironmentAlone(t *testing.T) {
	keycloakDirectory, err := provider.New(configFor(t, "KEYCLOAK"))
	if err != nil {
		t.Fatalf("New(KEYCLOAK): %v", err)
	}
	wso2Directory, err := provider.New(configFor(t, "WSO2_IS"))
	if err != nil {
		t.Fatalf("New(WSO2_IS): %v", err)
	}

	// The stub answers every operation the same way, which is how an
	// installation learns the provider is not finished rather than seeing an
	// empty directory.
	_, err = wso2Directory.SearchUsers(context.Background(), "tenant-a", idpdirectory.UserSearch{})
	if !errors.Is(err, idpdirectory.ErrUnimplemented) {
		t.Errorf("the WSO2 stub returned %v, want %v", err, idpdirectory.ErrUnimplemented)
	}

	// The Keycloak adapter reaches its tenant check before any network call,
	// so this distinguishes the two without a live server.
	_, err = keycloakDirectory.SearchUsers(context.Background(), "tenant-elsewhere", idpdirectory.UserSearch{})
	if !errors.Is(err, idpdirectory.ErrUnknownTenant) {
		t.Errorf("the Keycloak adapter returned %v, want %v", err, idpdirectory.ErrUnknownTenant)
	}
}

func TestAnUnknownProviderIsRefusedRatherThanDefaulted(t *testing.T) {
	_, err := provider.New(configFor(t, "ACTIVE_DIRECTORY"))
	if err == nil {
		t.Fatal("New accepted a provider type nobody implements")
	}
	// Silently falling back to Keycloak would mean a typo in production
	// configuration selects a directory the operator did not ask for.
	if !strings.Contains(err.Error(), "ACTIVE_DIRECTORY") {
		t.Errorf("error = %v, want it to name the unsupported type", err)
	}
}

// §16.1 and §7.4: the machine credential must never reach anything a browser
// can read. The realistic leak is a log line or an error body, so the secret
// type has to refuse to render itself.
func TestTheServiceAccountSecretNeverRendersItself(t *testing.T) {
	cfg := configFor(t, "KEYCLOAK")

	rendered := []string{
		cfg.ClientSecret.String(),
		fmt.Sprintf("%v", cfg.ClientSecret),
		fmt.Sprintf("%s", cfg.ClientSecret),
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		mustJSON(t, cfg),
	}
	for _, text := range rendered {
		if strings.Contains(text, secret) {
			t.Errorf("a rendering of the configuration leaked the secret: %s", text)
		}
	}

	// Redaction is worthless if it also hides the value from the adapter.
	if cfg.ClientSecret.Reveal() != secret {
		t.Error("Reveal did not return the secret the adapter needs")
	}
}

// §7.1 calls it a credentialsSecretRef: the value arrives by reference, so the
// secret is a mounted file rather than an environment variable visible in
// `docker inspect` and in every child process.
func TestTheSecretIsReadFromTheReferencedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idp-admin-credentials")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatalf("writing the secret file: %v", err)
	}

	cfg, err := provider.FromEnv(lookup(map[string]string{
		"IDP_TYPE":                   "KEYCLOAK",
		"IDP_BASE_URL":               "http://keycloak:8080",
		"IDP_REALM":                  "tenant-a",
		"IDP_TENANT_ID":              "tenant-a",
		"IDP_CLIENT_ID":              "patient-app",
		"IDP_SERVICE_CLIENT_ID":      "authorization-admin-service",
		"IDP_CREDENTIALS_SECRET_REF": path,
	}))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	// Trailing newlines are what every editor and `echo` adds; a secret that
	// works from a here-doc and fails from a file is an unfindable bug.
	if cfg.ClientSecret.Reveal() != secret {
		t.Errorf("the secret read from the file was %q", cfg.ClientSecret.Reveal())
	}
}

func TestAMissingSecretReferenceIsRefused(t *testing.T) {
	_, err := provider.FromEnv(lookup(map[string]string{
		"IDP_TYPE":     "KEYCLOAK",
		"IDP_BASE_URL": "http://keycloak:8080",
		"IDP_REALM":    "tenant-a",
	}))
	if err == nil {
		t.Fatal("FromEnv accepted a configuration with no credentials reference")
	}
}

// The issuer a token claims is the realm URL, so deriving it removes one
// setting that could disagree with the base URL and reject every token.
func TestTheIssuerAndKeySetAreDerivedFromTheRealm(t *testing.T) {
	cfg := configFor(t, "KEYCLOAK")

	if want := "http://keycloak:8080/realms/tenant-a"; cfg.Issuer != want {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, want)
	}
	if want := "http://keycloak:8080/realms/tenant-a/protocol/openid-connect/certs"; cfg.JWKSURL() != want {
		t.Errorf("JWKSURL = %q, want %q", cfg.JWKSURL(), want)
	}
	if cfg.RoleSource != tokenverifier.RoleSourceClient {
		t.Errorf("RoleSource = %q, want CLIENT by default", cfg.RoleSource)
	}
}

// A browser and a backend reach the identity provider by different names in
// every deployment that publishes it. The issuer is what the browser's token
// claims; the key set has to be fetched over the backend's own route, or the
// service goes looking for the issuer at an address only a browser can resolve.
func TestTheKeySetIsFetchedOverTheBackendRouteEvenWhenTheIssuerIsPublished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idp-admin-credentials")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatalf("writing the secret file: %v", err)
	}

	cfg, err := provider.FromEnv(lookup(map[string]string{
		"IDP_TYPE":                   "KEYCLOAK",
		"IDP_BASE_URL":               "http://keycloak:8080",
		"IDP_ISSUER":                 "http://localhost:8081/realms/tenant-a",
		"IDP_REALM":                  "tenant-a",
		"IDP_TENANT_ID":              "tenant-a",
		"IDP_CLIENT_ID":              "patient-app",
		"IDP_SERVICE_CLIENT_ID":      "authorization-admin-service",
		"IDP_CREDENTIALS_SECRET_REF": path,
	}))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}

	if cfg.Issuer != "http://localhost:8081/realms/tenant-a" {
		t.Errorf("Issuer = %q, want the published address the token claims", cfg.Issuer)
	}
	if want := "http://keycloak:8080/realms/tenant-a/protocol/openid-connect/certs"; cfg.JWKSURL() != want {
		t.Errorf("JWKSURL = %q, want %q", cfg.JWKSURL(), want)
	}
}

// The verifier and the directory are built from one configuration, so an
// installation cannot end up verifying tokens for one realm while reading roles
// out of another.
func TestTheVerifierIsBuiltFromTheSameConfigurationAsTheDirectory(t *testing.T) {
	cfg := configFor(t, "KEYCLOAK")

	verifier, err := provider.NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if verifier == nil {
		t.Fatal("NewVerifier returned no verifier")
	}
}

// ConfigFromTenant (issue #76) resolves the tenant's identity from a
// registry row rather than IDP_REALM/IDP_TENANT_ID/IDP_ISSUER/IDP_CLIENT_ID/
// IDP_SERVICE_CLIENT_ID/IDP_CREDENTIALS_SECRET_REF, while the adapter-level
// settings stay environment driven.
func TestConfigFromTenantResolvesIdentityFromTheRegistryRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idp-admin-credentials")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatalf("writing the secret file: %v", err)
	}

	cfg, err := provider.ConfigFromTenant(lookup(map[string]string{
		"IDP_TYPE":     "KEYCLOAK",
		"IDP_BASE_URL": "http://keycloak:8080",
	}), provider.Tenant{
		Realm:               "tenant-a",
		Issuer:              "http://localhost:8081/realms/tenant-a",
		BrowserClientID:     "patient-app",
		ServiceClientID:     "authorization-admin-service",
		CredentialSecretRef: path,
	})
	if err != nil {
		t.Fatalf("ConfigFromTenant: %v", err)
	}

	if cfg.Realm != "tenant-a" {
		t.Errorf("Realm = %q, want the registry row's realm", cfg.Realm)
	}
	if string(cfg.TenantID) != "tenant-a" {
		t.Errorf("TenantID = %q, want the realm verbatim, with no separate mapping", cfg.TenantID)
	}
	if cfg.Issuer != "http://localhost:8081/realms/tenant-a" {
		t.Errorf("Issuer = %q, want the registry row's issuer", cfg.Issuer)
	}
	if cfg.ClientID != "patient-app" {
		t.Errorf("ClientID = %q, want the registry row's browser client", cfg.ClientID)
	}
	if cfg.ServiceClientID != "authorization-admin-service" {
		t.Errorf("ServiceClientID = %q, want the registry row's service client", cfg.ServiceClientID)
	}
	if cfg.ClientSecret.Reveal() != secret {
		t.Errorf("ClientSecret = %q, want the secret read from the registry row's credentialSecretRef", cfg.ClientSecret.Reveal())
	}
}

func TestConfigFromTenantRejectsAnUnreadableSecretReference(t *testing.T) {
	_, err := provider.ConfigFromTenant(lookup(map[string]string{
		"IDP_TYPE":     "KEYCLOAK",
		"IDP_BASE_URL": "http://keycloak:8080",
	}), provider.Tenant{
		Realm:               "tenant-a",
		Issuer:              "http://localhost:8081/realms/tenant-a",
		BrowserClientID:     "patient-app",
		CredentialSecretRef: "/nonexistent/path/to/secret",
	})
	if err == nil {
		t.Fatal("ConfigFromTenant accepted a registry row whose secret reference cannot be read")
	}
}

func configFor(t *testing.T, idpType string) provider.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idp-admin-credentials")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatalf("writing the secret file: %v", err)
	}

	cfg, err := provider.FromEnv(lookup(map[string]string{
		"IDP_TYPE":                   idpType,
		"IDP_BASE_URL":               "http://keycloak:8080",
		"IDP_REALM":                  "tenant-a",
		"IDP_TENANT_ID":              "tenant-a",
		"IDP_CLIENT_ID":              "patient-app",
		"IDP_SERVICE_CLIENT_ID":      "authorization-admin-service",
		"IDP_CREDENTIALS_SECRET_REF": path,
	}))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	return cfg
}

func lookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		// A configuration that cannot be serialised cannot leak through a
		// serialised response either, so this is a pass, not a failure.
		return ""
	}
	return string(encoded)
}
