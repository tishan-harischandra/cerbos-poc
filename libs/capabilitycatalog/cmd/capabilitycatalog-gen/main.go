// Command capabilitycatalog-gen turns the 79-resource archetype set plus
// the committed §12.1 worked examples into the generated capability
// definitions file and the database catalog seed (issue #10).
//
// Usage:
//
//	go run ./libs/capabilitycatalog/cmd/capabilitycatalog-gen -root <repo-root> [-manifest <path>] [-check]
//
// Without -check it writes the generated tree and validates the full
// capability set. With -check it only reports whether the committed
// generated file already matches what the generator produces and whether
// the full set (generated + hand-authored) validates cleanly against the
// committed resource catalog - the CI gate for capability-catalog drift and
// validation violations (issue #10 acceptance criteria: "CI fails the build
// on any validation violation").
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

const (
	catalogResourcesDir = "deploy/cerbos/catalog/resources"
	capabilitiesDir     = "deploy/cerbos/catalog/ui-capabilities"
	generatedFileName   = "generated.yaml"
	seedDataFile        = "deploy/liquibase/changelog/data/ui_capability_definition.csv"
	seedChangeFile      = "deploy/liquibase/changelog/tables/008-ui-capability-seed.yaml"
)

// modulePath is where a module's generated document lives: one directory
// per module, so the ADS can read a module without reading its siblings.
func modulePath(dir, module string) string {
	return filepath.Join(dir, module, generatedFileName)
}

// writeModules writes each module's generated document, and removes a
// generated document for a module that no longer generates - otherwise a
// resource leaving the manifest would leave its capabilities serving
// forever.
func writeModules(dir string, byModule map[string]string) error {
	for module, doc := range byModule {
		path := modulePath(dir, module)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	stale, err := staleModules(dir, byModule)
	if err != nil {
		return err
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing stale %s: %w", path, err)
		}
	}
	return nil
}

// checkModules returns every generated document that differs from what the
// generator produces, is missing, or is left over from a module that no
// longer generates.
func checkModules(dir string, byModule map[string]string) []string {
	var mismatched []string
	for module, doc := range byModule {
		path := modulePath(dir, module)
		existing, err := os.ReadFile(path)
		if err != nil || string(existing) != doc {
			mismatched = append(mismatched, path)
		}
	}
	stale, err := staleModules(dir, byModule)
	if err != nil {
		return append(mismatched, err.Error())
	}
	mismatched = append(mismatched, stale...)
	sort.Strings(mismatched)
	return mismatched
}

// staleModules finds generated documents under dir whose module is absent
// from byModule.
func staleModules(dir string, byModule map[string]string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var stale []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, generated := byModule[entry.Name()]; generated {
			continue
		}
		path := modulePath(dir, entry.Name())
		if _, err := os.Stat(path); err == nil {
			stale = append(stale, path)
		}
	}
	return stale, nil
}

func main() {
	root := flag.String("root", ".", "repository root the output paths are relative to")
	manifestPath := flag.String("manifest", "",
		"path to the resource manifest (default <root>/"+cataloggen.DefaultManifestPath+")")
	check := flag.Bool("check", false, "verify the committed tree matches the generator and validates, instead of writing")
	flag.Parse()

	manifestFile := *manifestPath
	if manifestFile == "" {
		manifestFile = filepath.Join(*root, cataloggen.DefaultManifestPath)
	}

	manifest, err := cataloggen.LoadManifestFile(manifestFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: loading manifest: %v\n", err)
		os.Exit(1)
	}

	resources := capabilitycatalog.SelectArchetypeResources(manifest, capabilitycatalog.ArchetypeResourceCount)
	generated := capabilitycatalog.GenerateArchetypeCapabilities(resources, manifest.CatalogRevision)
	byModule := capabilitycatalog.RenderDefinitionsByModule(manifest.CatalogRevision, generated)

	handAuthoredDir := filepath.Join(*root, capabilitiesDir)
	handAuthored, err := capabilitycatalog.LoadDefinitionsDir(handAuthoredDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: loading hand-authored definitions: %v\n", err)
		os.Exit(1)
	}

	if *check {
		if mismatched := checkModules(handAuthoredDir, byModule); len(mismatched) > 0 {
			fmt.Fprintf(os.Stderr,
				"capabilitycatalog-gen: %v does not match the generator output; run "+
					"`go run ./libs/capabilitycatalog/cmd/capabilitycatalog-gen -root .`\n", mismatched)
			os.Exit(1)
		}
	} else {
		if err := writeModules(handAuthoredDir, byModule); err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: %v\n", err)
			os.Exit(1)
		}
		// The generated files are themselves input to LoadDefinitionsDir on
		// the next pass, so re-read the full set from disk after writing,
		// rather than trusting the in-memory slice, to catch a rendering bug
		// that a round trip through disk would expose.
		//
		// This must happen before any artifact exists. The loader prefers an
		// artifact when one is present, so building artifacts first would
		// make this read the gob and stop checking the YAML rendering that
		// is the whole point of re-reading.
		handAuthored, err = capabilitycatalog.LoadDefinitionsDir(handAuthoredDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: reloading definitions after write: %v\n", err)
			os.Exit(1)
		}
	}

	all := handAuthored
	catalogDir := filepath.Join(*root, catalogResourcesDir)
	catalog, err := capabilitycatalog.LoadActiveCatalogDir(catalogDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: loading resource catalog: %v\n", err)
		os.Exit(1)
	}

	if errs := capabilitycatalog.Validate(all, catalog); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: %d validation violation(s):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  %v\n", e)
		}
		os.Exit(1)
	}

	// Artifacts are emitted last, and only in write mode: after the YAML
	// they fingerprint, and after validation, so a catalog that does not
	// validate never gets a pre-decoded form for a service to load. This is
	// the sync output ADR-013 describes; it is git-ignored, never committed.
	if !*check {
		if err := capabilitycatalog.BuildModuleArtifacts(handAuthoredDir); err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: building artifacts: %v\n", err)
			os.Exit(1)
		}
	}

	seedCSV, err := capabilitycatalog.RenderSeedCSV(all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: rendering seed CSV: %v\n", err)
		os.Exit(1)
	}
	seedChangelog := capabilitycatalog.RenderSeedChangelog(manifest.CatalogRevision)

	seedDataPath := filepath.Join(*root, seedDataFile)
	seedChangePath := filepath.Join(*root, seedChangeFile)

	if *check {
		mismatched := []string{}
		if existing, err := os.ReadFile(seedDataPath); err != nil || string(existing) != seedCSV {
			mismatched = append(mismatched, seedDataFile)
		}
		if existing, err := os.ReadFile(seedChangePath); err != nil || string(existing) != seedChangelog {
			mismatched = append(mismatched, seedChangeFile)
		}
		if len(mismatched) > 0 {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: drift in %v; run "+
				"`go run ./libs/capabilitycatalog/cmd/capabilitycatalog-gen -root .`\n", mismatched)
			os.Exit(1)
		}
		fmt.Printf("capabilitycatalog-gen: %d capabilities match the committed tree and validate cleanly\n", len(all))
		return
	}

	if err := os.MkdirAll(filepath.Dir(seedDataPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(seedDataPath, []byte(seedCSV), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: writing %s: %v\n", seedDataPath, err)
		os.Exit(1)
	}
	if err := os.WriteFile(seedChangePath, []byte(seedChangelog), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: writing %s: %v\n", seedChangePath, err)
		os.Exit(1)
	}

	fmt.Printf("capabilitycatalog-gen: wrote %d capabilities under %s\n", len(all), *root)
}
