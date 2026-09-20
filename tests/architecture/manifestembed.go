// The adopter-data invariant (ADR-013, docs/specs/2026-09-16-adopter-sourced-domain-model.md).
//
// This is a platform: any SaaS should be able to adopt it. One SaaS's domain
// model - a 156-entry FHIR resource manifest - used to be //go:embed-ed into
// libs/cataloggen, which put it inside the platform binary and left no way
// for a second adopter to supply a different one without rebuilding. The
// generator now reads its manifest from a path. This file's test is what
// stops the embed from coming back, because re-adding one is a two-line
// change that nothing else would notice.
package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// ManifestGeneratorPackage is the repository-relative directory holding the
// catalog generator, including its command. Nothing under it may carry the
// domain model in the binary.
const ManifestGeneratorPackage = "libs/cataloggen"

// embedDirectivePrefix is the compiler directive that pulls a file into the
// binary at build time.
const embedDirectivePrefix = "//go:embed"

// embedPackage is the import a directive needs, blank or otherwise.
const embedPackage = "embed"

// ScanForEmbedDirective reports every way a file compiles content into the
// binary: a //go:embed directive, or an import of the embed package.
//
// Both are reported rather than either alone. A []byte or string embed needs
// only a blank import, so the directive is the reliable signal; an import
// with no directive is dead weight that a later edit would arm. Catching both
// means neither half can be reintroduced quietly.
func ScanForEmbedDirective(relativePath, source string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relativePath, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", relativePath, err)
	}

	var findings []Finding
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !strings.HasPrefix(comment.Text, embedDirectivePrefix) {
				continue
			}
			findings = append(findings, Finding{
				File:   relativePath,
				Line:   fset.Position(comment.Pos()).Line,
				Symbol: strings.TrimPrefix(embedDirectivePrefix, "//"),
				Message: "the adopter's domain model may not be compiled into a platform " +
					"artifact; read it from a configured path instead (ADR-013)",
			})
		}
	}

	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != embedPackage {
			continue
		}
		findings = append(findings, Finding{
			File:   relativePath,
			Line:   fset.Position(imported.Pos()).Line,
			Symbol: path,
			Message: "importing embed is how a //go:embed directive is armed; the catalog " +
				"generator has no file to embed (ADR-013)",
		})
	}

	return findings, nil
}
