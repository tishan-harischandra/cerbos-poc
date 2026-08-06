package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// Every administration query against a tenant-scoped table must carry a
// tenant_id predicate. This is the executable form of §21's isolation
// invariant for the read side: a query that could return another tenant's
// rows by accident is the failure mode the invariant exists to prevent.
func TestEveryAdministrationQueryNamesATenantPredicate(t *testing.T) {
	root := repoRoot(t)

	var findings []architecture.Finding
	for _, path := range goFiles(t, root) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relativising %s: %v", path, err)
		}
		if strings.HasPrefix(filepath.ToSlash(relative), "tests/architecture/") {
			continue
		}

		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		found, err := architecture.ScanForMissingTenantPredicate(relative, string(source))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, found...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("administration queries missing a tenant_id predicate:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAMissingTenantPredicate(t *testing.T) {
	const leak = `package postgresstore

func (s *Store) allRolePermissionsEverywhere(ctx context.Context) {
	const query = ` + "`" + `SELECT role_external_id, resource_key, action_key FROM role_permission` + "`" + `
	_ = query
}
`

	findings, err := architecture.ScanForMissingTenantPredicate("libs/assignmentstore/postgresstore/leak.go", leak)
	if err != nil {
		t.Fatalf("ScanForMissingTenantPredicate: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("the checker did not catch a role_permission query with no tenant_id predicate")
	}
	if findings[0].Symbol != "role_permission" {
		t.Errorf("finding named %q, want role_permission", findings[0].Symbol)
	}
}

// A point lookup by a globally unique event id must not be flagged: it
// cannot return another tenant's row no matter which tenant asks, because
// the caller can only have obtained that specific id in the first place.
func TestTheCheckerAllowsAPointLookupByEventID(t *testing.T) {
	const source = `package postgresstore

func (s *Store) AuditEvent(ctx context.Context, eventID string) {
	const query = ` + "`" + `SELECT actor_id FROM permission_audit_event WHERE event_id = $1` + "`" + `
	_ = query
}
`

	findings, err := architecture.ScanForMissingTenantPredicate("libs/assignmentstore/postgresstore/postgresstore.go", source)
	if err != nil {
		t.Fatalf("ScanForMissingTenantPredicate: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a point lookup by event id was flagged: %v", findings)
	}
}

// A query that does carry a tenant_id predicate must not be flagged.
func TestTheCheckerAllowsAQueryWithATenantPredicate(t *testing.T) {
	const source = `package postgresstore

func (s *Store) ActiveRolePermissions(ctx context.Context) {
	const query = ` + "`" + `SELECT action_key FROM role_permission WHERE tenant_id = $1` + "`" + `
	_ = query
}
`

	findings, err := architecture.ScanForMissingTenantPredicate("libs/assignmentstore/postgresstore/postgresstore.go", source)
	if err != nil {
		t.Fatalf("ScanForMissingTenantPredicate: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a query with a tenant_id predicate was flagged: %v", findings)
	}
}
