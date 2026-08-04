// Package architecture holds executable checks for the constraints that no
// compiler enforces.
//
// The constraint here is the project's most important one: permission
// precedence is expressed exclusively in Cerbos policy. Go code that reads the
// assembled action sets in order to decide an outcome is a defect even when it
// produces the right answer, because it creates the duplicated-logic failure
// mode §21 warns about.
package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// PermissionContextFields are the assembled action sets. Reading one of these in
// Go is how a precedence implementation starts: the moment code asks "is this
// action revoked?" it is ranking revokes against grants.
var PermissionContextFields = []string{
	"RoleGrantedActions",
	"UserGrantedActions",
	"UserRevokedActions",
}

// Finding is one violation of the precedence boundary.
type Finding struct {
	File    string
	Line    int
	Symbol  string
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Symbol, f.Message)
}

// ScanFile parses Go source and reports every read of an assembled action set.
//
// owner is the import path prefix allowed to touch the fields, namely the
// package that defines them. Everything else must treat the assembled context
// as opaque data to be handed to the PDP.
func ScanFile(filename, src string, isOwner bool) ([]Finding, error) {
	if isOwner {
		return nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	forbidden := make(map[string]struct{}, len(PermissionContextFields))
	for _, field := range PermissionContextFields {
		forbidden[field] = struct{}{}
	}

	var findings []Finding
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, isForbidden := forbidden[selector.Sel.Name]; !isForbidden {
			return true
		}
		findings = append(findings, Finding{
			File:   filename,
			Line:   fset.Position(selector.Sel.Pos()).Line,
			Symbol: selector.Sel.Name,
			Message: "precedence must live in Cerbos policy; Go code must not read " +
				"the assembled action sets to decide an outcome",
		})
		return true
	})

	return findings, nil
}

// IsOwner reports whether a path is allowed to read the action sets: the package
// that defines them, and test files, which assert the emitted data rather than
// deciding anything with it.
func IsOwner(relativePath string) bool {
	normalised := strings.ReplaceAll(relativePath, "\\", "/")
	if strings.HasSuffix(normalised, "_test.go") {
		return true
	}
	return strings.HasPrefix(normalised, "libs/permissioncontext/")
}
