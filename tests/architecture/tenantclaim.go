package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// ForbiddenTenantDerivations names identifiers whose mere presence in source
// means a claim-derived, request-derived or caller-chosen tenant mapping has
// come back (issue #77). §16.1 already made tenant and hospital context
// server-derived; #77 goes further for the tenant specifically - it is
// always the realm that signed the token, and there is no longer a mode,
// claim name or per-request field that could name a different one. Any of
// these identifiers reappearing is that regression, whichever package it
// shows up in.
var ForbiddenTenantDerivations = []string{
	"TenantMappingMode",
	"TenantMappingClaim",
	"TenantMappingRealm",
	"TenantClaim",
}

// ScanForClaimDerivedTenant reports every reference to a
// ForbiddenTenantDerivations identifier.
func ScanForClaimDerivedTenant(filename, src string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	forbidden := make(map[string]bool, len(ForbiddenTenantDerivations))
	for _, name := range ForbiddenTenantDerivations {
		forbidden[name] = true
	}

	var findings []Finding
	ast.Inspect(file, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || !forbidden[ident.Name] {
			return true
		}
		findings = append(findings, Finding{
			File:   filename,
			Line:   fset.Position(ident.Pos()).Line,
			Symbol: ident.Name,
			Message: fmt.Sprintf(
				"%q reintroduces a claim- or mode-selected tenant mapping; the tenant is always the realm that signed the token (issue #77)",
				ident.Name),
		})
		return true
	})
	return findings, nil
}
