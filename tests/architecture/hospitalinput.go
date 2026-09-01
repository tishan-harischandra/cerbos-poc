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

var hospitalLiteral = regexp.MustCompile(`(?i)hospital`)

// hospitalReadingCalls names the HTTP-input accessors a hospital identifier
// must never be read through (issue #78): §16.1 already requires tenant and
// hospital context to be derived server-side, from the verified token, and
// none of these methods ever touch a token.
var hospitalReadingCalls = map[string]bool{
	"Get":       true, // (*http.Header).Get, url.Values.Get
	"FormValue": true, // (*http.Request).FormValue
}

// hospitalSearchFilterFiles are administration search endpoints that take a
// hospital as a *search filter*, the same way they take a tenant, an actor
// or a resource key: a caller narrows a query, authority.Validate checks
// the narrowing is within the caller's own tenant/hospital scope, and the
// store answers only within it (§8.2). This is not the identity-derivation
// path §16.1 and issue #78 are about - the caller's own hospital still
// comes only from the verified token, checked before the filter is ever
// used - so these are named exceptions rather than a hole in the rule.
var hospitalSearchFilterFiles = map[string]bool{
	"apps/admin-service/internal/auditsearch/handler.go": true,
}

// ScanForRequestDerivedHospital reports every call to a header, query
// string or form accessor whose argument names a hospital, which is what a
// hospital read from request input looks like in source. It does not scan
// for a hospital field on a request body: this codebase's decision
// endpoint already refuses any unrecognised body field outright
// (json.Decoder.DisallowUnknownFields), so a body-smuggled hospital is
// caught before it is ever this far.
func ScanForRequestDerivedHospital(filename, src string) ([]Finding, error) {
	if hospitalSearchFilterFiles[filename] {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	var findings []Finding
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !hospitalReadingCalls[selector.Sel.Name] {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value := literalValueHospital(lit.Value)
		if !hospitalLiteral.MatchString(value) {
			return true
		}
		findings = append(findings, Finding{
			File:   filename,
			Line:   fset.Position(call.Pos()).Line,
			Symbol: selector.Sel.Name,
			Message: fmt.Sprintf(
				"%s(%q) reads a hospital from request input; the hospital comes only from the verified token (§16.1, issue #78)",
				selector.Sel.Name, value),
		})
		return true
	})
	return findings, nil
}

func literalValueHospital(raw string) string {
	if strings.HasPrefix(raw, "`") {
		return strings.Trim(raw, "`")
	}
	unquoted, err := strconv.Unquote(raw)
	if err != nil {
		return raw
	}
	return unquoted
}
