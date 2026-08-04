package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
)

// ContextFields are the four things the permissionContext may carry: three
// action sets and the revision they were read at.
//
// They are also the resource schema Cerbos validates the attribute against, so
// the list is a contract with the policy tree rather than a Go detail. Adding a
// field on this side alone gives the policies something they cannot read;
// removing one leaves a policy condition reading an attribute that never
// arrives, which Cerbos answers by denying every action.
var ContextFields = []string{
	"RoleGrantedActions",
	"UserGrantedActions",
	"UserRevokedActions",
	"PermissionRevision",
}

// verdictVocabulary is the language of having already decided. A field named in
// it means the ADS reached an outcome in Go, which is precisely what §6.3 and
// ADR-003 place in Cerbos policy instead.
var verdictVocabulary = regexp.MustCompile(
	`(?i)(allow|den(y|ied)|permit|forbid|verdict|decision|effect|outcome|authorized|authorised)`)

// ScanPermissionContextShape reports any way the wire context has stopped being
// pure data: a field the schema does not declare, a declared field that has gone
// missing, or a field that names a verdict.
func ScanPermissionContextShape(filename, src string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	declared := make(map[string]struct{}, len(ContextFields))
	for _, field := range ContextFields {
		declared[field] = struct{}{}
	}

	var findings []Finding
	present := make(map[string]struct{}, len(ContextFields))
	found := false

	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Context" {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		found = true

		for _, field := range structType.Fields.List {
			for _, name := range field.Names {
				position := fset.Position(name.Pos()).Line
				if verdictVocabulary.MatchString(name.Name) {
					findings = append(findings, Finding{
						File:   filename,
						Line:   position,
						Symbol: name.Name,
						Message: "the permission context reports facts; a verdict " +
							"belongs to the PDP (§6.3, ADR-003)",
					})
					continue
				}
				if _, ok := declared[name.Name]; !ok {
					findings = append(findings, Finding{
						File:   filename,
						Line:   position,
						Symbol: name.Name,
						Message: "the permission context is a contract with the Cerbos " +
							"resource schema; a field added here alone cannot be read in policy",
					})
					continue
				}
				present[name.Name] = struct{}{}
			}
		}
		return false
	})

	if !found {
		return nil, fmt.Errorf("%s declares no Context type to check", filename)
	}

	var missing []string
	for _, field := range ContextFields {
		if _, ok := present[field]; !ok {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	for _, field := range missing {
		findings = append(findings, Finding{
			File:   filename,
			Symbol: field,
			Message: "the Cerbos resource schema declares this attribute; without it " +
				"every policy condition reading it denies",
		})
	}

	return findings, nil
}
