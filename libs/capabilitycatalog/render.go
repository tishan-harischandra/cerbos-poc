package capabilitycatalog

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// generatedHeader mirrors libs/cataloggen's convention (policy.go) so a
// reviewer never mistakes generated output for a hand-authored file.
const generatedHeader = "" +
	"# GENERATED FILE - DO NOT EDIT BY HAND.\n" +
	"#\n" +
	"# Produced by libs/capabilitycatalog's archetype generator (catalog\n" +
	"# revision %d, issue #10, PRD \"UI capability catalog\"). Edit\n" +
	"# libs/cataloggen/manifest.yaml or the archetype definitions in\n" +
	"# libs/capabilitycatalog/generator.go and re-run `make capability-gen`,\n" +
	"# or regenerate directly with\n" +
	"# `go run ./libs/capabilitycatalog/cmd/capabilitycatalog-gen -root .`.\n" +
	"#\n" +
	"# The five hand-authored §12.1 worked examples live alongside this file\n" +
	"# in clinical-worked-examples.yaml and are never touched by the generator.\n"

// RenderDefinitionsYAML renders the mechanically generated capabilities as a
// definitionFile document (the same shape LoadDefinitionsDir reads).
func RenderDefinitionsYAML(catalogRevision int64, defs []UiCapabilityDefinition) string {
	var b strings.Builder
	fmt.Fprintf(&b, generatedHeader, catalogRevision)
	fmt.Fprintf(&b, "catalogRevision: %d\n", catalogRevision)
	b.WriteString("capabilities:\n")
	for _, d := range defs {
		fmt.Fprintf(&b, "  - key: %s\n", d.Key)
		fmt.Fprintf(&b, "    module: %s\n", d.Module)
		fmt.Fprintf(&b, "    context: %s\n", d.Context)
		b.WriteString("    expression:\n")
		renderExpressionYAML(&b, d.Expression, "      ")
	}
	return b.String()
}

// renderExpressionYAML renders one expression node at the given indent,
// recursing into allOf/anyOf children. Kept hand-rolled (rather than
// gopkg.in/yaml.v3's generic encoder) so the output is stable, readable and
// matches the exact shape UnmarshalYAML/LoadDefinitionsDir expects.
func renderExpressionYAML(b *strings.Builder, e Expression, indent string) {
	switch {
	case e.Permission != nil:
		fmt.Fprintf(b, "%spermission:\n", indent)
		fmt.Fprintf(b, "%s  resource: %s\n", indent, e.Permission.Resource)
		fmt.Fprintf(b, "%s  action: %s\n", indent, e.Permission.Action)
		fmt.Fprintf(b, "%s  targetRef: %s\n", indent, e.Permission.TargetRef)
	case e.AllOf != nil:
		fmt.Fprintf(b, "%sallOf:\n", indent)
		for _, child := range e.AllOf {
			fmt.Fprintf(b, "%s  - ", indent)
			renderExpressionYAMLInline(b, child, indent+"    ")
		}
	case e.AnyOf != nil:
		fmt.Fprintf(b, "%sanyOf:\n", indent)
		for _, child := range e.AnyOf {
			fmt.Fprintf(b, "%s  - ", indent)
			renderExpressionYAMLInline(b, child, indent+"    ")
		}
	case e.CapabilityRef != "":
		fmt.Fprintf(b, "%scapabilityRef: %s\n", indent, e.CapabilityRef)
	}
}

// renderExpressionYAMLInline renders a sequence item's first line without a
// leading indent (the caller already wrote "  - "), then any further lines
// at indent.
func renderExpressionYAMLInline(b *strings.Builder, e Expression, indent string) {
	var inner strings.Builder
	renderExpressionYAML(&inner, e, indent)
	lines := strings.SplitAfter(inner.String(), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		if i == 0 {
			b.WriteString(strings.TrimPrefix(line, indent))
		} else {
			b.WriteString(line)
		}
	}
}

// RenderSeedCSV renders one row per capability for a Liquibase loadData of
// the ui_capability_definition table
// (deploy/liquibase/changelog/tables/002-authorization-catalog.yaml),
// following the column order the table declares.
func RenderSeedCSV(defs []UiCapabilityDefinition) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write([]string{"capability_key", "module_key", "context_type", "expression_json", "catalog_revision", "enabled"}); err != nil {
		return "", err
	}
	for _, d := range defs {
		exprJSON, err := json.Marshal(d.Expression)
		if err != nil {
			return "", fmt.Errorf("marshalling expression for %s: %w", d.Key, err)
		}
		row := []string{
			d.Key,
			d.Module,
			d.Context,
			string(exprJSON),
			strconv.FormatInt(d.CatalogRevision, 10),
			"true",
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("writing seed row for %s: %w", d.Key, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderSeedChangelog renders the Liquibase changeset that bulk loads the
// ui_capability_definition CSV, mirroring
// libs/cataloggen/seed.go's RenderSeedChangelog.
func RenderSeedChangelog(catalogRevision int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, generatedHeader, catalogRevision)
	b.WriteString("databaseChangeLog:\n")
	b.WriteString("  - changeSet:\n")
	b.WriteString("      id: 008-ui-capability-definition-seed\n")
	b.WriteString("      author: capabilitycatalog\n")
	b.WriteString("      comment: >-\n")
	b.WriteString("        The generated and hand-authored UI capability catalog (issue #10),\n")
	b.WriteString("        loaded from CSV so the same portable changeset works on both engines.\n")
	b.WriteString("      changes:\n")
	b.WriteString("        - loadData:\n")
	b.WriteString("            tableName: ui_capability_definition\n")
	b.WriteString("            file: ../data/ui_capability_definition.csv\n")
	b.WriteString("            relativeToChangelogFile: true\n")
	b.WriteString("            columns:\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: capability_key\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: module_key\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: context_type\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: expression_json\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: catalog_revision\n")
	b.WriteString("                  type: NUMERIC\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: enabled\n")
	b.WriteString("                  type: BOOLEAN\n")
	return b.String()
}
