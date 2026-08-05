package cataloggen

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

// RenderResourceSeedCSV renders one row per included resource for a
// Liquibase loadData of the authorization_resource table (§8.1,
// deploy/liquibase/changelog/tables/002-authorization-catalog.yaml).
func RenderResourceSeedCSV(m *Manifest) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write([]string{"resource_key", "version", "display_name", "domain", "catalog_revision"}); err != nil {
		return "", err
	}
	for _, entry := range m.IncludedResources() {
		row := []string{
			entry.ResourceKey,
			"v1",
			entry.Display,
			entry.Domain,
			strconv.FormatInt(m.CatalogRevision, 10),
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("writing resource seed row for %s: %w", entry.FHIRType, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderActionSeedCSV renders one row per (resource, action) pair for a
// Liquibase loadData of the authorization_action table.
func RenderActionSeedCSV(m *Manifest) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write([]string{"resource_key", "action_key", "display_name", "context_type", "risk_level"}); err != nil {
		return "", err
	}
	for _, entry := range m.IncludedResources() {
		for _, action := range m.Actions {
			risk := "STANDARD"
			if isLockable(m, action.Key) {
				risk = "ELEVATED"
			}
			row := []string{
				entry.ResourceKey,
				action.Key,
				fmt.Sprintf("%s %s", action.DisplayName, strings.ToLower(entry.Display)),
				action.Context,
				risk,
			}
			if err := w.Write(row); err != nil {
				return "", fmt.Errorf("writing action seed row for %s/%s: %w", entry.FHIRType, action.Key, err)
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderSeedChangelog renders the Liquibase changeset that bulk loads both
// CSVs. loadData is Liquibase's dialect-agnostic bulk insert, which is why
// the catalog seed is data files plus one small changeset rather than one
// generated insert changeset per row.
func RenderSeedChangelog(m *Manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, generatedHeader, m.CatalogRevision)
	b.WriteString("databaseChangeLog:\n")
	b.WriteString("  - changeSet:\n")
	b.WriteString("      id: 007-authorization-resource-seed\n")
	b.WriteString("      author: cataloggen\n")
	b.WriteString("      comment: >-\n")
	b.WriteString("        The generated FHIR resource catalog (issue #8), loaded from CSV so the\n")
	b.WriteString("        same portable changeset works on both engines without one generated\n")
	b.WriteString("        insert changeset per row.\n")
	b.WriteString("      changes:\n")
	b.WriteString("        - loadData:\n")
	b.WriteString("            tableName: authorization_resource\n")
	b.WriteString("            file: ../data/authorization_resource.csv\n")
	b.WriteString("            relativeToChangelogFile: true\n")
	b.WriteString("            columns:\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: resource_key\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: version\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: display_name\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: domain\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: catalog_revision\n")
	b.WriteString("                  type: NUMERIC\n\n")

	b.WriteString("  - changeSet:\n")
	b.WriteString("      id: 007-authorization-action-seed\n")
	b.WriteString("      author: cataloggen\n")
	b.WriteString("      comment: One row per (resource, action) pair in the generated catalog.\n")
	b.WriteString("      changes:\n")
	b.WriteString("        - loadData:\n")
	b.WriteString("            tableName: authorization_action\n")
	b.WriteString("            file: ../data/authorization_action.csv\n")
	b.WriteString("            relativeToChangelogFile: true\n")
	b.WriteString("            columns:\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: resource_key\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: action_key\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: display_name\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: context_type\n")
	b.WriteString("                  type: STRING\n")
	b.WriteString("              - column:\n")
	b.WriteString("                  name: risk_level\n")
	b.WriteString("                  type: STRING\n")

	return b.String()
}
