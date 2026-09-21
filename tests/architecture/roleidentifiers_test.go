package architecture_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The constraint: every canonical role identifier hardcoded in this
// repository names a role the seeded realms actually declare.
//
// Without this, a change to the role model leaves stale identifiers behind
// that only fail at runtime, in a smoke suite, as an unrelated-looking
// error. That is exactly how issue #110 happened: d7c92c8 moved roles from
// client to realm, and four identifiers naming `patient-app` client roles
// survived for months as red CI.
//
// Identifiers naming a realm this repository does not seed are skipped:
// unit tests invent realms freely and are not asserting anything about the
// deployed configuration.
func TestHardcodedRoleIdentifiersResolveAgainstTheSeededRealms(t *testing.T) {
	root := repoRoot(t)
	realms := seededRealms(t, root)
	if len(realms) == 0 {
		t.Fatalf("no realm configurations under %s; the scan would pass vacuously",
			architecture.SeededRealmDir)
	}

	var problems []string
	var checked int

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		// Shell scripts drive the real seeded stack, and non-test Go carries
		// the seeds and generators that must agree with it.
		//
		// Unit tests are deliberately excluded. They invent realms, clients
		// and roles to exercise a library in isolation - libs/tokenverifier's
		// suite has to name a client role precisely because it tests
		// RoleSourceClient - and holding their fixtures to the deployed
		// realm's contents would forbid testing a configuration this
		// installation does not happen to use.
		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".sh" && ext != ".go" {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// This gate's own fixtures name roles on purpose.
		if strings.HasPrefix(relative, "tests/architecture/roleidentifiers") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		for _, id := range architecture.ScanForRoleIdentifiers(relative, string(source)) {
			realm, seeded := realms[id.Realm]
			if !seeded {
				continue
			}
			checked++
			if realm.HasRole(id.Source, id.Role) {
				continue
			}
			problems = append(problems, describe(id, realm))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}

	if checked == 0 {
		t.Fatal("no role identifiers naming a seeded realm were found; the gate would pass vacuously")
	}

	if len(problems) > 0 {
		var report strings.Builder
		report.WriteString("canonical role identifiers name roles the seeded realms do not declare:\n")
		for _, p := range problems {
			report.WriteString("  " + p + "\n")
		}
		t.Fatal(report.String())
	}
}

func describe(id architecture.RoleIdentifier, realm *architecture.SeededRealm) string {
	if id.Source == "realm" {
		return fmt.Sprintf("%s:%d: %s - realm %q declares no realm role %q",
			id.File, id.Line, id, id.Realm, id.Role)
	}
	clients := realm.ClientsWithRoles()
	if len(clients) == 0 {
		return fmt.Sprintf("%s:%d: %s - realm %q declares no client roles at all, so this "+
			"must be a realm role: kc:%s:realm:%s",
			id.File, id.Line, id, id.Realm, id.Realm, id.Role)
	}
	return fmt.Sprintf("%s:%d: %s - realm %q declares no role %q on client %q (clients with roles: %v)",
		id.File, id.Line, id, id.Realm, id.Role, id.Source, clients)
}

func seededRealms(t *testing.T, root string) map[string]*architecture.SeededRealm {
	t.Helper()
	dir := filepath.Join(root, architecture.SeededRealmDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	realms := map[string]*architecture.SeededRealm{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		realm, err := architecture.LoadSeededRealm(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("loading %s: %v", entry.Name(), err)
		}
		if realm.Realm != "" {
			realms[realm.Realm] = realm
		}
	}
	return realms
}

func skipDir(name string) bool {
	switch name {
	case "node_modules", ".git", "dist", ".nx", ".angular", ".gocache", "build", "tmp":
		return true
	}
	return false
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerFindsRoleIdentifiers(t *testing.T) {
	const source = `role="kc:tenant-a:patient-app:auditor"
other='kc:tenant-a:realm:doctor'
forged="kc:tenant-a:realm:sys:forged-evaluator"
`

	found := architecture.ScanForRoleIdentifiers("scripts/example.sh", source)
	if len(found) != 3 {
		t.Fatalf("found = %d, want 3; got %v", len(found), found)
	}

	if found[0].Source != "patient-app" || found[0].Role != "auditor" {
		t.Errorf("client role parsed as %+v", found[0])
	}
	if found[1].Source != "realm" || found[1].Role != "doctor" {
		t.Errorf("realm role parsed as %+v", found[1])
	}
	// A role name may itself contain a colon, so the role segment must not
	// stop at the first one.
	if found[2].Role != "sys:forged-evaluator" {
		t.Errorf("role with a colon parsed as %q, want sys:forged-evaluator", found[2].Role)
	}
	if found[0].Line != 1 || found[2].Line != 3 {
		t.Errorf("line numbers wrong: %+v", found)
	}
}

func TestTheCheckerResolvesAgainstARealmConfiguration(t *testing.T) {
	path := filepath.Join(repoRoot(t), architecture.SeededRealmDir, "realm-tenant-a.json")
	realm, err := architecture.LoadSeededRealm(path)
	if err != nil {
		t.Fatalf("LoadSeededRealm: %v", err)
	}

	if !realm.HasRole("realm", "doctor") {
		t.Errorf("tenant-a should declare the realm role doctor")
	}
	if realm.HasRole("patient-app", "doctor") {
		t.Errorf("tenant-a should declare no client role doctor on patient-app; " +
			"that is the model d7c92c8 retired")
	}
	if realm.HasRole("realm", "no-such-role") {
		t.Errorf("an undeclared role must not resolve")
	}
}
