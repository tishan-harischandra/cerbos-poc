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

	// The committed file names its issuer and credential secret path as
	// ${TENANT_ISSUER} / ${TENANT_CREDENTIAL_SECRET_REF} rather than a fixed
	// value: a locally overridden Keycloak hostname/port, or this seeder's
	// own container mounting the repository at a different path than the
	// ADS and Admin Service do, must not require editing the checked-in
	// file. Substituting before Parse (rather than after) means the
	// credential secret ref validation Parse performs always checks the
	// path this process will actually read.
	issuer := os.Getenv("TENANT_ISSUER")
	if issuer == "" {
		issuer = "http://localhost:8081/realms/tenant-a"
	}
	credentialSecretRef := os.Getenv("TENANT_CREDENTIAL_SECRET_REF")
	if credentialSecretRef == "" {
		credentialSecretRef = "/run/secrets/idp-admin-credentials"
	}
	raw = bytes.ReplaceAll(raw, []byte("${TENANT_ISSUER}"), []byte(issuer))
	raw = bytes.ReplaceAll(raw, []byte("${TENANT_CREDENTIAL_SECRET_REF}"), []byte(credentialSecretRef))

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
