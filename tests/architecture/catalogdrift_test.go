package architecture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// TestGeneratedCatalogTreeMatchesTheManifest is the CI gate for
// catalog-policy drift (§21) and for the "regenerating from an unchanged
// manifest produces a byte-identical tree" acceptance criterion (issue #8).
// If this fails, someone edited a generated file under deploy/cerbos or
// deploy/liquibase/changelog by hand, or changed the manifest without
// running the generator.
func TestGeneratedCatalogTreeMatchesTheManifest(t *testing.T) {
	manifest, err := cataloggen.LoadEmbeddedManifest()
	if err != nil {
		t.Fatalf("loading the committed manifest: %v", err)
	}

	files, err := cataloggen.Generate(manifest, architecture.CatalogOutputPaths())
	if err != nil {
		t.Fatalf("generating from the committed manifest: %v", err)
	}

	root := repoRoot(t)
	mismatched, err := cataloggen.Diff(root, files)
	if err != nil {
		t.Fatalf("diffing the generated tree against disk: %v", err)
	}

	if len(mismatched) > 0 {
		t.Fatalf("the committed catalog tree does not match libs/cataloggen/manifest.yaml "+
			"(%d file(s)); run `go run ./libs/cataloggen/cmd/cataloggen` at the repo root:\n%v",
			len(mismatched), mismatched)
	}
}

var grantToRolePattern = regexp.MustCompile(`(?m)^\s*-\s*name:\s*grant_(\w+)_to_role\s*$`)

var catalogActionKeyPattern = regexp.MustCompile(`(?m)^\s*-\s*key:\s*(\S+)\s*$`)

// TestEveryPolicyResourceHasACatalogEntryAndViceVersa is the two-way half of
// the §21 drift gate, read directly off disk rather than off the manifest:
// every Cerbos resource policy under deploy/cerbos/policies/resources has a
// matching catalog file with the same action set, and every catalog file has
// a matching policy, so a hand-edit cannot add or remove an action from one
// side of the boundary without the other noticing.
func TestEveryPolicyResourceHasACatalogEntryAndViceVersa(t *testing.T) {
	root := repoRoot(t)
	policyActions := actionsByResource(t, filepath.Join(root, "deploy/cerbos/policies/resources"), grantToRolePattern)
	catalogActions := actionsByResource(t, filepath.Join(root, "deploy/cerbos/catalog/resources"), catalogActionKeyPattern)

	for key, actions := range policyActions {
		catalog, ok := catalogActions[key]
		if !ok {
			t.Errorf("resource %q has a policy (deploy/cerbos/policies/resources) but no catalog entry "+
				"(deploy/cerbos/catalog/resources)", key)
			continue
		}
		if diff := setDiff(actions, catalog); len(diff) > 0 {
			t.Errorf("resource %q policy grants actions the catalog does not declare: %v", key, diff)
		}
	}
	for key, actions := range catalogActions {
		policy, ok := policyActions[key]
		if !ok {
			t.Errorf("resource %q has a catalog entry (deploy/cerbos/catalog/resources) but no policy "+
				"(deploy/cerbos/policies/resources)", key)
			continue
		}
		if diff := setDiff(actions, policy); len(diff) > 0 {
			t.Errorf("resource %q catalog declares actions the policy has no rule for: %v", key, diff)
		}
	}
}

// actionsByResource maps each non-test YAML file's resource key (its file
// name without extension) to the set of action names extracted by pattern,
// whose first capture group is the action key.
func actionsByResource(t *testing.T, dir string, pattern *regexp.Regexp) map[string]map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	result := make(map[string]map[string]bool, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.yaml") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		key := strings.TrimSuffix(name, ".yaml")

		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", filepath.Join(dir, name), err)
		}

		matches := pattern.FindAllStringSubmatch(string(content), -1)
		actions := make(map[string]bool, len(matches))
		for _, m := range matches {
			actions[m[1]] = true
		}
		result[key] = actions
	}
	return result
}

func setDiff(a, b map[string]bool) []string {
	var diff []string
	for k := range a {
		if !b[k] {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	return diff
}
