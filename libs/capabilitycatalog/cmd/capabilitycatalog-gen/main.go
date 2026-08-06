// Command capabilitycatalog-gen turns the 79-resource archetype set plus
// the committed §12.1 worked examples into the generated capability
// definitions file and the database catalog seed (issue #10).
//
// Usage:
//
//	go run ./libs/capabilitycatalog/cmd/capabilitycatalog-gen -root <repo-root> [-check]
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

func main() {
	root := flag.String("root", ".", "repository root the output paths are relative to")
	check := flag.Bool("check", false, "verify the committed tree matches the generator and validates, instead of writing")
	flag.Parse()

	manifest, err := cataloggen.LoadEmbeddedManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: loading manifest: %v\n", err)
		os.Exit(1)
	}

	resources := capabilitycatalog.SelectArchetypeResources(manifest, capabilitycatalog.ArchetypeResourceCount)
	generated := capabilitycatalog.GenerateArchetypeCapabilities(resources, manifest.CatalogRevision)
	generatedYAML := capabilitycatalog.RenderDefinitionsYAML(manifest.CatalogRevision, generated)

	handAuthoredDir := filepath.Join(*root, capabilitiesDir)
	handAuthored, err := capabilitycatalog.LoadDefinitionsDir(handAuthoredDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: loading hand-authored definitions: %v\n", err)
		os.Exit(1)
	}

	if *check {
		generatedPath := filepath.Join(*root, capabilitiesDir, generatedFileName)
		existing, readErr := os.ReadFile(generatedPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: reading %s: %v\n", generatedPath, readErr)
			os.Exit(1)
		}
		if string(existing) != generatedYAML {
			fmt.Fprintf(os.Stderr,
				"capabilitycatalog-gen: %s does not match the generator output; run "+
					"`go run ./libs/capabilitycatalog/cmd/capabilitycatalog-gen -root .`\n", generatedPath)
			os.Exit(1)
		}
	} else {
		generatedPath := filepath.Join(*root, capabilitiesDir, generatedFileName)
		if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(generatedPath, []byte(generatedYAML), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-gen: writing %s: %v\n", generatedPath, err)
			os.Exit(1)
		}
		// generated.yaml is itself hand-authored input to LoadDefinitionsDir on
		// the next pass, so re-read the full set from disk after writing it,
		// rather than trusting the in-memory slice, to catch a rendering bug
		// that a round trip through disk would expose.
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
