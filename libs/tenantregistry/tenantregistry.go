// Package tenantregistry parses and validates the declarative file that
// names every tenant realm this installation trusts (issue #76).
//
// The realm is the tenant identifier verbatim: there is no separate tenant
// column and no mapping layer. Parsing here does no network access and
// touches no database; it is the pure, unit-testable half of the registry.
// Seeding the parsed entries into the database of record is a separate step
// (see the Seed function), so that "does this file parse and validate"
// never depends on a live Postgres or Oracle connection.
package tenantregistry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Entry is one tenant realm this installation trusts.
//
// Realm is the tenant identifier verbatim (§7.1's tenantMappingMode contract
// is deliberately gone): a token's issuer names the realm, and the realm
// name is the tenant ID a decision is made against. No field here maps one
// string to another.
type Entry struct {
	// Realm is both the Keycloak realm name and the tenant identifier.
	Realm string
	// Issuer is what a token from this realm must claim. It is the published
	// address a browser resolves, which is not always the same host a
	// backend reaches the identity provider by.
	Issuer string
	// BrowserClientID is the public client whose audience a browser-minted
	// token carries.
	BrowserClientID string
	// ServiceClientID is the confidential client this installation
	// authenticates to the identity provider as, to read the directory.
	ServiceClientID string
	// CredentialSecretRef is a path to a mounted file holding the service
	// client's secret - by reference, never by value, so the secret is
	// never visible in `docker inspect` or inherited by a child process.
	CredentialSecretRef string
}

// fileEntry mirrors the YAML shape of one registry file row.
type fileEntry struct {
	Realm               string `yaml:"realm"`
	Issuer              string `yaml:"issuer"`
	BrowserClientID     string `yaml:"browserClientId"`
	ServiceClientID     string `yaml:"serviceClientId"`
	CredentialSecretRef string `yaml:"credentialSecretRef"`
}

// ParseFile reads and validates the registry file at path.
//
// Validation never touches the network: a missing or unreadable
// credentialSecretRef is checked by reading the file, the same as the
// per-service identity configuration already does, but nothing here
// contacts the identity provider itself.
func ParseFile(path string) ([]Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tenantregistry: reading %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse validates and returns the entries in a registry file's contents.
func Parse(raw []byte) ([]Entry, error) {
	var rows []fileEntry
	if err := yaml.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("tenantregistry: parsing the registry file: %w", err)
	}

	seen := make(map[string]bool, len(rows))
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		if row.Realm == "" {
			return nil, fmt.Errorf("tenantregistry: a registry entry has no realm")
		}
		if seen[row.Realm] {
			return nil, fmt.Errorf("tenantregistry: realm %q is declared more than once", row.Realm)
		}
		seen[row.Realm] = true

		if row.Issuer == "" {
			return nil, fmt.Errorf("tenantregistry: realm %q has no issuer", row.Realm)
		}
		if row.BrowserClientID == "" {
			return nil, fmt.Errorf("tenantregistry: realm %q has no browserClientId", row.Realm)
		}
		if row.CredentialSecretRef == "" {
			return nil, fmt.Errorf("tenantregistry: realm %q has no credentialSecretRef", row.Realm)
		}
		if _, err := os.ReadFile(row.CredentialSecretRef); err != nil {
			return nil, fmt.Errorf("tenantregistry: realm %q's credentialSecretRef %q is not readable: %w", row.Realm, row.CredentialSecretRef, err)
		}

		serviceClientID := row.ServiceClientID
		if serviceClientID == "" {
			serviceClientID = row.BrowserClientID
		}

		entries = append(entries, Entry{
			Realm:               row.Realm,
			Issuer:              row.Issuer,
			BrowserClientID:     row.BrowserClientID,
			ServiceClientID:     serviceClientID,
			CredentialSecretRef: row.CredentialSecretRef,
		})
	}

	return entries, nil
}
