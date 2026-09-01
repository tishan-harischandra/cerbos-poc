package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

// organizationAdapterFiles are the identity directory adapters' own files -
// the "adapter surface" issue #85's acceptance criterion names. Keycloak
// stays the only place an organization or a membership is created or
// changed; this platform only ever reads them.
var organizationAdapterFiles = map[string]bool{
	"libs/idpdirectory/keycloak/keycloak.go": true,
	"libs/idpdirectory/wso2/wso2.go":         true,
}

var organizationFuncName = regexp.MustCompile(`(?i)organization`)

// writeHTTPMethods are the net/http method constants a read never needs.
var writeHTTPMethods = map[string]bool{
	"MethodPost":   true,
	"MethodPut":    true,
	"MethodPatch":  true,
	"MethodDelete": true,
}

// ScanForOrganizationWrite reports a write HTTP method used inside a
// function whose name is about organizations or their memberships. It is
// scoped to those functions, not the whole file, so a legitimate write
// elsewhere in the same adapter - minting the service-account token, for
// instance - is not mistaken for one.
func ScanForOrganizationWrite(filename, src string) ([]Finding, error) {
	if !organizationAdapterFiles[strings.ReplaceAll(filename, "\\", "/")] {
		return nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !organizationFuncName.MatchString(fn.Name.Name) {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "http" || !writeHTTPMethods[selector.Sel.Name] {
				return true
			}
			findings = append(findings, Finding{
				File:   filename,
				Line:   fset.Position(selector.Pos()).Line,
				Symbol: fn.Name.Name + ": http." + selector.Sel.Name,
				Message: "no write path to organizations or memberships exists in this platform " +
					"(issue #85); Keycloak is the sole system of record",
			})
			return true
		})
	}
	return findings, nil
}
