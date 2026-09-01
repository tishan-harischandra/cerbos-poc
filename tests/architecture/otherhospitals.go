package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// OtherHospitalsField is the token field that carries a user's memberships
// other than the active hospital (issue #84). It is display data for a
// hospital switcher; reading it anywhere a decision is made would let a
// membership list widen access the same way a forged hospital claim would.
const OtherHospitalsField = "OtherHospitals"

// ScanForOtherHospitalsRead parses Go source and reports every read of the
// OtherHospitals field outside the package that owns it.
func ScanForOtherHospitalsRead(filename, src string, isOwner bool) ([]Finding, error) {
	if isOwner {
		return nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	var findings []Finding
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != OtherHospitalsField {
			return true
		}
		findings = append(findings, Finding{
			File:   filename,
			Line:   fset.Position(selector.Sel.Pos()).Line,
			Symbol: selector.Sel.Name,
			Message: "OtherHospitals is display data for a hospital switcher (issue #84); " +
				"no decision path may read it",
		})
		return true
	})

	return findings, nil
}

// IsOtherHospitalsOwner reports whether a path is allowed to read the field:
// the package that defines it, and test files, which assert the emitted
// data rather than deciding anything with it.
func IsOtherHospitalsOwner(relativePath string) bool {
	normalised := strings.ReplaceAll(relativePath, "\\", "/")
	if strings.HasSuffix(normalised, "_test.go") {
		return true
	}
	return strings.HasPrefix(normalised, "libs/tokenverifier/")
}
