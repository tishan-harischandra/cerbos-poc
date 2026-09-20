package architecture_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The constraint: no adopter's domain model is compiled into a platform
// artifact (ADR-013). The generator reads its manifest from a path it is
// given; the moment it embeds one, a 156-entry FHIR resource list is inside
// the platform binary again and no second adopter can supply a different one
// without rebuilding it.
func TestTheManifestGeneratorEmbedsNothing(t *testing.T) {
	root := repoRoot(t)
	packageDir := filepath.Join(root, architecture.ManifestGeneratorPackage)

	var scanned int
	var findings []architecture.Finding
	err := filepath.WalkDir(packageDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		fileFindings, err := architecture.ScanForEmbedDirective(relative, string(source))
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
		scanned++
		return nil
	})
	if err != nil {
		t.Fatalf("scanning %s: %v", architecture.ManifestGeneratorPackage, err)
	}

	if scanned == 0 {
		t.Fatalf("no non-test Go files under %s; the scan would pass vacuously",
			architecture.ManifestGeneratorPackage)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("the domain model is compiled into the platform again:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// The path the generators default to has to be a manifest that is actually
// there, or every one of them fails on a default nobody changed.
func TestTheDefaultManifestPathExists(t *testing.T) {
	path := filepath.Join(repoRoot(t), cataloggen.DefaultManifestPath)

	if _, err := cataloggen.LoadManifestFile(path); err != nil {
		t.Fatalf("the default manifest path does not load: %v", err)
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAnEmbedDirective(t *testing.T) {
	const violation = `package cataloggen

import "embed"

//go:embed manifest.yaml
var embeddedManifest embed.FS
`

	findings, err := architecture.ScanForEmbedDirective("libs/cataloggen/manifest.go", violation)
	if err != nil {
		t.Fatalf("ScanForEmbedDirective: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2 (the directive and the import); got %v", len(findings), findings)
	}

	symbols := map[string]bool{}
	for _, finding := range findings {
		symbols[finding.Symbol] = true
	}
	for _, want := range []string{"go:embed", "embed"} {
		if !symbols[want] {
			t.Errorf("no finding names %q; got %v", want, findings)
		}
	}
}

// A string or []byte embed needs no reference to the embed package beyond a
// blank import, so catching only the import would miss the directive and
// catching only `embed.FS` would miss both.
func TestTheCheckerCatchesABlankEmbedImport(t *testing.T) {
	const violation = `package cataloggen

import _ "embed"

//go:embed manifest.yaml
var manifestYAML string
`

	findings, err := architecture.ScanForEmbedDirective("libs/cataloggen/manifest.go", violation)
	if err != nil {
		t.Fatalf("ScanForEmbedDirective: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2; got %v", len(findings), findings)
	}
}

// Reading the manifest from the filesystem is the whole point of the change,
// so the rule must not forbid it.
func TestTheCheckerAllowsAFilesystemRead(t *testing.T) {
	const allowed = `package cataloggen

import (
	"fmt"
	"os"
)

func LoadManifestFile(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	return ParseManifest(raw)
}
`

	findings, err := architecture.ScanForEmbedDirective("libs/cataloggen/manifest.go", allowed)
	if err != nil {
		t.Fatalf("ScanForEmbedDirective: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a filesystem read was flagged: %v", findings)
	}
}
