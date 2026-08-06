package cataloggen

import (
	"fmt"
	"strings"
)

// RenderCatalogEntry renders the administration-facing resource/action
// catalog document for one manifest entry, following the §6.1 format:
//
//	resource: patient_record
//	version: v1
//	displayName: Patient record
//	domain: clinical
//	actions:
//	  - key: list
//	    displayName: List patients
//	    context: COLLECTION
func RenderCatalogEntry(m *Manifest, entry ResourceEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, generatedHeader, m.CatalogRevision)
	fmt.Fprintf(&b, "resource: %s\n", entry.ResourceKey)
	b.WriteString("version: v1\n")
	fmt.Fprintf(&b, "displayName: %s\n", entry.Display)
	fmt.Fprintf(&b, "domain: %s\n", entry.Domain)
	fmt.Fprintf(&b, "fhirType: %s\n", entry.FHIRType)
	b.WriteString("actions:\n")
	for _, action := range m.Actions {
		fmt.Fprintf(&b, "  - key: %s\n", action.Key)
		fmt.Fprintf(&b, "    displayName: %s %s\n", action.DisplayName, strings.ToLower(entry.Display))
		fmt.Fprintf(&b, "    context: %s\n", action.Context)
		// Same classification the DB seed's risk_level column uses (see
		// RenderActionSeedCSV): one lockableActions list drives both, so
		// the catalog the browser reads and the row the database stores
		// can never name a different risk for the same action.
		risk := "STANDARD"
		if isLockable(m, action.Key) {
			risk = "ELEVATED"
		}
		fmt.Fprintf(&b, "    risk: %s\n", risk)
	}
	return b.String()
}
