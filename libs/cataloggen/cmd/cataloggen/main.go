// Command cataloggen turns libs/cataloggen/manifest.yaml into the generated
// catalog, Cerbos policies, JSON schemas, Cerbos test suite and database
// catalog seed (issue #8).
//
// Usage:
//
//	go run ./libs/cataloggen/cmd/cataloggen -root <repo-root> [-check]
//
// Without -check it writes the generated tree. With -check it only reports
// whether the committed tree already matches what the manifest generates,
// which is the CI gate for catalog-policy drift and for the "regenerating
// produces a byte-identical tree" acceptance criterion.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

func main() {
	root := flag.String("root", ".", "repository root the output paths are relative to")
	check := flag.Bool("check", false, "verify the committed tree matches the manifest instead of writing")
	flag.Parse()

	manifest, err := cataloggen.LoadEmbeddedManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cataloggen: %v\n", err)
		os.Exit(1)
	}

	paths := cataloggen.OutputPaths{
		CatalogDir:     "deploy/cerbos/catalog/resources",
		PolicyDir:      "deploy/cerbos/policies/resources",
		SchemaDir:      "deploy/cerbos/policies/_schemas",
		TestDir:        "deploy/cerbos/policies/tests",
		SeedDataDir:    "deploy/liquibase/changelog/data",
		SeedChangeFile: "deploy/liquibase/changelog/tables/007-catalog-seed.yaml",
	}

	files, err := cataloggen.Generate(manifest, paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cataloggen: generating: %v\n", err)
		os.Exit(1)
	}

	if *check {
		mismatched, err := cataloggen.Diff(*root, files)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cataloggen: %v\n", err)
			os.Exit(1)
		}
		if len(mismatched) > 0 {
			fmt.Fprintf(os.Stderr,
				"cataloggen: the committed tree does not match the manifest (%d file(s)):\n",
				len(mismatched))
			for _, m := range mismatched {
				fmt.Fprintf(os.Stderr, "  %s\n", m)
			}
			fmt.Fprintln(os.Stderr, "run: go run ./libs/cataloggen/cmd/cataloggen -root . ")
			os.Exit(1)
		}
		fmt.Printf("cataloggen: %d generated files match the committed tree\n", len(files))
		return
	}

	if err := cataloggen.WriteAll(*root, files); err != nil {
		fmt.Fprintf(os.Stderr, "cataloggen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("cataloggen: wrote %d files under %s\n", len(files), *root)
}
