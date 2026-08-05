// The catalog-policy drift gate (issue #8, §21 "catalog-policy drift").
//
// The generated Cerbos policies, JSON schemas, Cerbos test suite and
// database catalog seed under deploy/ are committed output of
// libs/cataloggen plus libs/cataloggen/manifest.yaml. This file's test
// regenerates that tree in memory from the very same manifest the package
// embeds and diffs it against what is on disk, so a manifest change with no
// matching regeneration - or a hand-edit of a generated file - fails CI
// instead of silently drifting from the catalog it claims to implement.
package architecture

import (
	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

// CatalogOutputPaths mirrors libs/cataloggen/cmd/cataloggen's output layout.
// Kept here, rather than imported, so a change to the CLI's paths has to be
// deliberately mirrored in the one test that checks the committed tree
// still matches it.
func CatalogOutputPaths() cataloggen.OutputPaths {
	return cataloggen.OutputPaths{
		CatalogDir:     "deploy/cerbos/catalog/resources",
		PolicyDir:      "deploy/cerbos/policies/resources",
		SchemaDir:      "deploy/cerbos/policies/_schemas",
		TestDir:        "deploy/cerbos/policies/tests",
		SeedDataDir:    "deploy/liquibase/changelog/data",
		SeedChangeFile: "deploy/liquibase/changelog/tables/007-catalog-seed.yaml",
	}
}
