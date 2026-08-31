// Command seed-tenants writes the tenant registry file's entries into the
// authorization database (issue #76).
//
// It runs as a container step of `make up`, the same as the demo role
// matrix seeder: the registry is file-seeded but database-of-record, so
// every start reconciles the table with the file rather than reading the
// file directly at request time.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/tenantseed"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "seed-tenants: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("seed-tenants: the tenant registry is in place")
}

func run() error {
	dsn := os.Getenv("ASSIGNMENTSTORE_POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("ASSIGNMENTSTORE_POSTGRES_DSN is not set")
	}
	registryFile := os.Getenv("TENANT_REGISTRY_FILE")
	if registryFile == "" {
		return fmt.Errorf("TENANT_REGISTRY_FILE is not set")
	}

	raw, err := os.ReadFile(registryFile)
	if err != nil {
		return fmt.Errorf("reading %s: %w", registryFile, err)
	}

	// The committed file names each realm's issuer and credential secret
	// path as ${TENANT_<REALM>_ISSUER} / ${TENANT_<REALM>_CREDENTIAL_SECRET_REF}
	// rather than a fixed value: a locally overridden Keycloak
	// hostname/port, or this seeder's own container mounting the
	// repository at a different path than the ADS and Admin Service do,
	// must not require editing the checked-in file. Substituting before
	// Parse (rather than after) means the credential secret ref validation
	// Parse performs always checks the path this process will actually
	// read.
	//
	// One deployment serves every realm the file names (issue #77), so the
	// substitution is generic rather than naming tenant-a specifically:
	// every TENANT_<REALM>_ISSUER in the environment is substituted for
	// its own ${TENANT_<REALM>_ISSUER} placeholder, and likewise for
	// _CREDENTIAL_SECRET_REF.
	raw = substituteTenantPlaceholders(raw)

	entries, err := tenantregistry.Parse(raw)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store, err := postgresstore.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	if err := store.Ping(ctx); err != nil {
		return fmt.Errorf("the authorization database is not reachable: %w", err)
	}

	return tenantseed.Apply(ctx, store, entries)
}

// tenantEnvVar matches TENANT_<REALM>_ISSUER and TENANT_<REALM>_CREDENTIAL_SECRET_REF,
// capturing the realm portion so its value substitutes the placeholder of
// the same shape in the registry file.
var tenantEnvVar = regexp.MustCompile(`^TENANT_(.+)_(ISSUER|CREDENTIAL_SECRET_REF)$`)

// substituteTenantPlaceholders replaces every ${TENANT_<REALM>_ISSUER} and
// ${TENANT_<REALM>_CREDENTIAL_SECRET_REF} placeholder in raw with the value
// of the environment variable of the same name. tenant-a's placeholders
// fall back to the values compose has always defaulted them to, so a
// registry file with no environment overrides at all still seeds.
func substituteTenantPlaceholders(raw []byte) []byte {
	defaults := map[string]string{
		"TENANT_A_ISSUER":                "http://localhost:8081/realms/tenant-a",
		"TENANT_A_CREDENTIAL_SECRET_REF": "/run/secrets/idp-admin-credentials",
	}

	seen := make(map[string]bool, len(defaults))
	for _, env := range os.Environ() {
		name, value, ok := strings.Cut(env, "=")
		if !ok || !tenantEnvVar.MatchString(name) {
			continue
		}
		seen[name] = true
		raw = bytes.ReplaceAll(raw, []byte("${"+name+"}"), []byte(value))
	}
	for name, value := range defaults {
		if seen[name] {
			continue
		}
		raw = bytes.ReplaceAll(raw, []byte("${"+name+"}"), []byte(value))
	}
	return raw
}
