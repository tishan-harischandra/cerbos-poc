package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The check that matters: nothing in the repository outside the Cerbos policy
// tree may rank grants against revokes.
func TestNoGoCodeEncodesPrecedenceOrdering(t *testing.T) {
	root := repoRoot(t)

	var findings []architecture.Finding
	for _, path := range goFiles(t, root) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relativising %s: %v", path, err)
		}

		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		fileFindings, err := architecture.ScanFile(relative, string(source), architecture.IsOwner(relative))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, fileFindings...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("precedence logic found outside the Cerbos policy tree:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration. This drives a deliberate
// violation through the same checker the repository scan uses.
func TestTheCheckerCatchesAPrecedenceImplementation(t *testing.T) {
	const violation = `package ads

import "example.com/permissioncontext"

// The defect this whole constraint exists to prevent.
func allowed(ctx permissioncontext.Context, action string) bool {
	for _, revoked := range ctx.UserRevokedActions {
		if revoked == action {
			return false
		}
	}
	for _, granted := range ctx.RoleGrantedActions {
		if granted == action {
			return true
		}
	}
	return false
}
`

	findings, err := architecture.ScanFile("apps/ads/internal/authz/decide.go", violation, false)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2; got %v", len(findings), findings)
	}

	symbols := map[string]bool{}
	for _, finding := range findings {
		symbols[finding.Symbol] = true
	}
	for _, want := range []string{"UserRevokedActions", "RoleGrantedActions"} {
		if !symbols[want] {
			t.Errorf("the checker missed %s; found %v", want, symbols)
		}
	}
}

func TestThePackageDefiningTheFieldsMayReadThem(t *testing.T) {
	const owner = `package permissioncontext

func (c Context) empty() bool {
	return len(c.RoleGrantedActions) == 0
}
`

	findings, err := architecture.ScanFile("libs/permissioncontext/permissioncontext.go", owner, true)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("the owning package was flagged: %v", findings)
	}
}

func TestOwnershipRules(t *testing.T) {
	cases := map[string]bool{
		"libs/permissioncontext/permissioncontext.go": true,
		"apps/ads/internal/authz/authz_test.go":       true,
		"apps/ads/internal/authz/authz.go":            false,
		"libs/cerbosclient/cerbosclient.go":           false,
		"apps/ads/cmd/ads/main.go":                    false,
	}

	for path, want := range cases {
		if got := architecture.IsOwner(path); got != want {
			t.Errorf("IsOwner(%q) = %t, want %t", path, got, want)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// tests/architecture -> repository root
	root := filepath.Dir(filepath.Dir(working))
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Fatalf("could not locate the repository root from %s: %v", working, err)
	}
	return root
}

func goFiles(t *testing.T, root string) []string {
	t.Helper()

	skipDirs := map[string]struct{}{
		"node_modules": {},
		".git":         {},
		"dist":         {},
		".gocache":     {},
		"testdata":     {},
		"vendor":       {},
	}

	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if _, skip := skipDirs[entry.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if len(paths) == 0 {
		t.Fatal("no Go files found; the scan would pass vacuously")
	}
	return paths
}
