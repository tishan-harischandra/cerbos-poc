package main

import (
	"os"
	"strings"
	"testing"
)

// Two realms in the same registry file (issue #77) each get their own
// issuer and credential secret ref substituted from the environment
// variable of the matching name.
func TestSubstituteTenantPlaceholdersSubstitutesEachRealmIndependently(t *testing.T) {
	t.Setenv("TENANT_A_ISSUER", "http://keycloak:8080/realms/tenant-a")
	t.Setenv("TENANT_A_CREDENTIAL_SECRET_REF", "/run/secrets/idp-admin-credentials")
	t.Setenv("TENANT_B_ISSUER", "http://keycloak:8080/realms/tenant-b")
	t.Setenv("TENANT_B_CREDENTIAL_SECRET_REF", "/run/secrets/idp-admin-credentials-tenant-b")

	raw := []byte(`- realm: tenant-a
  issuer: "${TENANT_A_ISSUER}"
  credentialSecretRef: "${TENANT_A_CREDENTIAL_SECRET_REF}"
- realm: tenant-b
  issuer: "${TENANT_B_ISSUER}"
  credentialSecretRef: "${TENANT_B_CREDENTIAL_SECRET_REF}"
`)

	got := string(substituteTenantPlaceholders(raw))

	for _, want := range []string{
		"http://keycloak:8080/realms/tenant-a",
		"/run/secrets/idp-admin-credentials\"",
		"http://keycloak:8080/realms/tenant-b",
		"/run/secrets/idp-admin-credentials-tenant-b",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("substituted registry = %s, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "${TENANT") {
		t.Errorf("substituted registry still names a placeholder: %s", got)
	}
}

// tenant-a's placeholders keep working with no environment overrides at
// all, the same default compose has always used, so the committed file
// still seeds on a bare `make up`.
func TestSubstituteTenantPlaceholdersFallsBackToTenantADefaults(t *testing.T) {
	for _, name := range []string{"TENANT_A_ISSUER", "TENANT_A_CREDENTIAL_SECRET_REF"} {
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}

	raw := []byte(`- realm: tenant-a
  issuer: "${TENANT_A_ISSUER}"
  credentialSecretRef: "${TENANT_A_CREDENTIAL_SECRET_REF}"
`)

	got := string(substituteTenantPlaceholders(raw))
	if !strings.Contains(got, "http://localhost:8081/realms/tenant-a") {
		t.Errorf("substituted registry = %s, want the default tenant-a issuer", got)
	}
	if !strings.Contains(got, "/run/secrets/idp-admin-credentials\"") {
		t.Errorf("substituted registry = %s, want the default tenant-a secret ref", got)
	}
}
