package cataloggen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// OutputPaths names every directory and file the generator writes to. All
// paths are relative to the repository root.
type OutputPaths struct {
	CatalogDir     string // one <resource>.yaml per resource (§6.1)
	PolicyDir      string // one <resource>.yaml Cerbos resource policy per resource
	SchemaDir      string // one <resource>.json per resource
	TestDir        string // <resource>_test.yaml and <resource>_schema_test.yaml per resource
	SeedDataDir    string // authorization_resource.csv, authorization_action.csv
	SeedChangeFile string // the Liquibase changeset that loads the CSVs
}

// GeneratedFile is one file this package can produce, in memory, before it is
// written to disk. Keeping generation and writing separate is what makes the
// golden-file test and the CI drift check able to compare content without
// touching the filesystem.
type GeneratedFile struct {
	// RelativePath is relative to the repository root.
	RelativePath string
	Content      string
}

// Generate renders every output file for the included resources in the
// manifest, sorted by resource key so the result is always in the same
// order: a manifest change is the only thing that changes the generated
// tree (the "regenerating produces a byte-identical tree" acceptance
// criterion).
func Generate(m *Manifest, paths OutputPaths) ([]GeneratedFile, error) {
	included := m.IncludedResources()
	sort.Slice(included, func(i, j int) bool {
		return included[i].ResourceKey < included[j].ResourceKey
	})

	var files []GeneratedFile

	for _, entry := range included {
		files = append(files, GeneratedFile{
			RelativePath: filepath.Join(paths.CatalogDir, entry.ResourceKey+".yaml"),
			Content:      RenderCatalogEntry(m, entry),
		})
		files = append(files, GeneratedFile{
			RelativePath: filepath.Join(paths.PolicyDir, entry.ResourceKey+".yaml"),
			Content:      RenderPolicy(m, entry),
		})

		schema, err := RenderSchema(entry)
		if err != nil {
			return nil, fmt.Errorf("rendering schema for %s: %w", entry.FHIRType, err)
		}
		files = append(files, GeneratedFile{
			RelativePath: filepath.Join(paths.SchemaDir, entry.ResourceKey+".json"),
			Content:      schema,
		})

		files = append(files, GeneratedFile{
			RelativePath: filepath.Join(paths.TestDir, entry.ResourceKey+"_test.yaml"),
			Content:      RenderPrecedenceTest(m, entry),
		})
		files = append(files, GeneratedFile{
			RelativePath: filepath.Join(paths.TestDir, entry.ResourceKey+"_schema_test.yaml"),
			Content:      RenderSchemaTest(m, entry),
		})
	}

	resourceCSV, err := RenderResourceSeedCSV(m)
	if err != nil {
		return nil, fmt.Errorf("rendering resource seed CSV: %w", err)
	}
	actionCSV, err := RenderActionSeedCSV(m)
	if err != nil {
		return nil, fmt.Errorf("rendering action seed CSV: %w", err)
	}
	files = append(files,
		GeneratedFile{
			RelativePath: filepath.Join(paths.SeedDataDir, "authorization_resource.csv"),
			Content:      resourceCSV,
		},
		GeneratedFile{
			RelativePath: filepath.Join(paths.SeedDataDir, "authorization_action.csv"),
			Content:      actionCSV,
		},
		GeneratedFile{
			RelativePath: paths.SeedChangeFile,
			Content:      RenderSeedChangelog(m),
		},
	)

	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	return files, nil
}

// WriteAll writes every generated file under root, creating directories as
// needed.
func WriteAll(root string, files []GeneratedFile) error {
	for _, f := range files {
		full := filepath.Join(root, f.RelativePath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", f.RelativePath, err)
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", f.RelativePath, err)
		}
	}
	return nil
}

// Diff compares generated files against what is already on disk under root,
// returning the relative paths that differ or are missing. An empty result
// means regenerating produced a byte-identical tree.
func Diff(root string, files []GeneratedFile) ([]string, error) {
	var mismatched []string
	for _, f := range files {
		full := filepath.Join(root, f.RelativePath)
		existing, err := os.ReadFile(full)
		if os.IsNotExist(err) {
			mismatched = append(mismatched, f.RelativePath+" (missing)")
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.RelativePath, err)
		}
		if string(existing) != f.Content {
			mismatched = append(mismatched, f.RelativePath+" (content differs)")
		}
	}
	return mismatched, nil
}
