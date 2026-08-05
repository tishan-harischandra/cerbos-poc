package cataloggen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

func loadFixtureManifest(t *testing.T) *cataloggen.Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fixture_manifest.yaml"))
	if err != nil {
		t.Fatalf("reading fixture manifest: %v", err)
	}
	m, err := cataloggen.ParseManifest(raw)
	if err != nil {
		t.Fatalf("parsing fixture manifest: %v", err)
	}
	return m
}

func fixturePaths() cataloggen.OutputPaths {
	return cataloggen.OutputPaths{
		CatalogDir:     "catalog",
		PolicyDir:      "policies",
		SchemaDir:      "schemas",
		TestDir:        "tests",
		SeedDataDir:    "seed-data",
		SeedChangeFile: "seed/007-catalog-seed.yaml",
	}
}

// TestGenerateMatchesGoldenFiles is the golden-file test the PRD asks for: a
// generator change surfaces here as a reviewable diff, because the golden
// files under testdata/golden are committed. Run with
// UPDATE_GOLDEN=1 go test ./... to regenerate them after an intentional
// template change.
func TestGenerateMatchesGoldenFiles(t *testing.T) {
	m := loadFixtureManifest(t)
	files, err := cataloggen.Generate(m, fixturePaths())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(files) == 0 {
		t.Fatalf("Generate produced no files")
	}

	goldenRoot := filepath.Join("testdata", "golden")
	update := os.Getenv("UPDATE_GOLDEN") != ""

	for _, f := range files {
		goldenPath := filepath.Join(goldenRoot, f.RelativePath)

		if update {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
				t.Fatalf("creating golden dir for %s: %v", f.RelativePath, err)
			}
			if err := os.WriteFile(goldenPath, []byte(f.Content), 0o644); err != nil {
				t.Fatalf("writing golden file %s: %v", f.RelativePath, err)
			}
			continue
		}

		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("reading golden file %s (run with UPDATE_GOLDEN=1 to create it): %v",
				goldenPath, err)
		}
		if string(want) != f.Content {
			t.Errorf("generated content for %s does not match golden file %s", f.RelativePath, goldenPath)
		}
	}
}

// TestGenerateIsDeterministic proves the "regenerating from an unchanged
// manifest produces a byte-identical tree" acceptance criterion at the unit
// level: two calls to Generate on the same manifest must produce identical
// file lists in identical order with identical content.
func TestGenerateIsDeterministic(t *testing.T) {
	m := loadFixtureManifest(t)

	first, err := cataloggen.Generate(m, fixturePaths())
	if err != nil {
		t.Fatalf("Generate (first): %v", err)
	}
	second, err := cataloggen.Generate(m, fixturePaths())
	if err != nil {
		t.Fatalf("Generate (second): %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("file count differs across runs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].RelativePath != second[i].RelativePath {
			t.Fatalf("file order differs at index %d: %s vs %s", i, first[i].RelativePath, second[i].RelativePath)
		}
		if first[i].Content != second[i].Content {
			t.Fatalf("content for %s differs across runs", first[i].RelativePath)
		}
	}
}

// TestGenerateExcludesResourcesMarkedIncludedFalse proves the manifest's
// inclusion flag is honoured: the fixture excludes Practitioner, so nothing
// with its resource key should appear anywhere in the generated tree.
func TestGenerateExcludesResourcesMarkedIncludedFalse(t *testing.T) {
	m := loadFixtureManifest(t)
	files, err := cataloggen.Generate(m, fixturePaths())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, f := range files {
		if filepathContains(f.RelativePath, "practitioner") {
			t.Fatalf("excluded resource practitioner leaked into generated file %s", f.RelativePath)
		}
	}
}

func filepathContains(path, substr string) bool {
	for i := 0; i+len(substr) <= len(path); i++ {
		if path[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestDiffReportsMissingAndDifferingFiles exercises the CI drift check path:
// Diff must report a file that does not exist on disk yet, and a file whose
// content has drifted from what the manifest currently generates.
func TestDiffReportsMissingAndDifferingFiles(t *testing.T) {
	m := loadFixtureManifest(t)
	files, err := cataloggen.Generate(m, fixturePaths())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	root := t.TempDir()
	if err := cataloggen.WriteAll(root, files); err != nil {
		t.Fatalf("WriteAll: %v", err)
	}

	if mismatched, err := cataloggen.Diff(root, files); err != nil {
		t.Fatalf("Diff on freshly written tree: %v", err)
	} else if len(mismatched) != 0 {
		t.Fatalf("Diff reported drift on a freshly written tree: %v", mismatched)
	}

	drifted := files[0]
	driftedPath := filepath.Join(root, drifted.RelativePath)
	if err := os.WriteFile(driftedPath, []byte("stale content"), 0o644); err != nil {
		t.Fatalf("corrupting %s: %v", driftedPath, err)
	}
	if err := os.Remove(filepath.Join(root, files[len(files)-1].RelativePath)); err != nil {
		t.Fatalf("removing %s: %v", files[len(files)-1].RelativePath, err)
	}

	mismatched, err := cataloggen.Diff(root, files)
	if err != nil {
		t.Fatalf("Diff after drift: %v", err)
	}
	if len(mismatched) != 2 {
		t.Fatalf("expected 2 mismatched files, got %d: %v", len(mismatched), mismatched)
	}
}
