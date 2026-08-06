package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// TenantScopedTables are the §8.2 tables an administration query lists or
// browses by tenant. A SELECT against one of these that names no tenant_id
// predicate could return another tenant's rows by accident - exactly the
// isolation failure §21's invariant exists to prevent.
//
// permission_audit_event and outbox_event are deliberately not on this list:
// every SELECT against them in the adapters looks up one row by its own
// globally unique event_id, which a caller can only have obtained by already
// holding that specific id. That is a point lookup, not the kind of
// administration query - a list, a search, a page - this check is for.
var TenantScopedTables = []string{
	"role_permission",
	"user_permission_override",
	"fhir_resource",
	"permission_revision",
}

var tenantIDPattern = regexp.MustCompile(`(?i)\btenant_id\b`)

func selectFromTablePattern(table string) *regexp.Regexp {
	return regexp.MustCompile(`(?is)\bSELECT\b.*?\bFROM\s+` + regexp.QuoteMeta(table) + `\b`)
}

// ScanForMissingTenantPredicate reports every SQL string literal that reads a
// TenantScopedTables table without mentioning tenant_id anywhere in the same
// statement.
func ScanForMissingTenantPredicate(filename, src string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	var findings []Finding
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value := literalValue(lit.Value)

		for _, table := range TenantScopedTables {
			if !selectFromTablePattern(table).MatchString(value) {
				continue
			}
			if tenantIDPattern.MatchString(value) {
				continue
			}
			findings = append(findings, Finding{
				File:   filename,
				Line:   fset.Position(lit.Pos()).Line,
				Symbol: table,
				Message: fmt.Sprintf(
					"a SELECT from %s names no tenant_id predicate; %s is a tenant-scoped table (§8.2, §21)",
					table, table),
			})
		}
		return true
	})
	return findings, nil
}

// literalValue unquotes a Go string literal, backtick-raw or double-quoted,
// so the SQL inside can be pattern-matched as written.
func literalValue(raw string) string {
	if strings.HasPrefix(raw, "`") {
		return strings.Trim(raw, "`")
	}
	unquoted, err := strconv.Unquote(raw)
	if err != nil {
		return raw
	}
	return unquoted
}
