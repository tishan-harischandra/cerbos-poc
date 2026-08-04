package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// servedPolicyDir is the bundle the running PDP loads from disk.
const servedPolicyDir = "deploy/cerbos/policies"

// The ADR-003 control experiment deliberately reproduces the cross-role hazard:
// a deny on one role defeated by an allow on another. It belongs to the proof,
// not to the deployment, and lives in deploy/cerbos/control so the PDP never
// loads it. Nothing but this test stops it drifting back.
func TestTheServedPolicyBundleCarriesNoNonProductionPolicy(t *testing.T) {
	root := repoRoot(t)
	bundle := filepath.Join(root, servedPolicyDir)

	var offenders []string
	err := filepath.WalkDir(bundle, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !isPolicyFile(entry.Name()) {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), "Not a production policy") {
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				relative = path
			}
			offenders = append(offenders, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", servedPolicyDir, err)
	}

	if len(offenders) > 0 {
		t.Fatalf("policies marked as non-production are in the bundle the PDP serves: %v",
			offenders)
	}
}

// A bundle with no policies at all would make the test above pass while proving
// nothing, and would mean the PDP is serving an empty rule set.
func TestTheServedPolicyBundleIsNotEmpty(t *testing.T) {
	root := repoRoot(t)
	bundle := filepath.Join(root, servedPolicyDir, "resources")

	entries, err := os.ReadDir(bundle)
	if err != nil {
		t.Fatalf("reading %s: %v", bundle, err)
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && isPolicyFile(entry.Name()) {
			count++
		}
	}
	if count == 0 {
		t.Fatalf("no resource policies found in %s/resources", servedPolicyDir)
	}
}

func isPolicyFile(name string) bool {
	if strings.HasSuffix(name, "_test.yaml") {
		return false
	}
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}
