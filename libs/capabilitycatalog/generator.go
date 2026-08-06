package capabilitycatalog

import (
	"fmt"
	"sort"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

// HospitalContextResource is the cross-cutting tenant/hospital scoping leaf
// every X.route.list archetype requires (PRD "UI capability catalog";
// deploy/cerbos/catalog/resources/hospital_context.yaml).
const HospitalContextResource = "hospital_context"

// ArchetypeCount is the number of archetypes instantiated per resource
// (PRD: "instantiating five archetypes across eighty resources").
const ArchetypeCount = 5

// SelectArchetypeResources returns the first n resources (by resource key)
// from the manifest's included resources, deterministically. patient_record
// is already excluded because it is not part of cataloggen's
// IncludedResources() (it stays hand-authored per libs/cataloggen/manifest.yaml).
//
// The PRD's "eighty resources" is reconciled against the "exactly 400
// capabilities" acceptance criterion by generating 79 resources
// mechanically (79 * 5 = 395) and adding the five hand-authored §12.1
// worked examples on top, for exactly 400 (issue #10).
func SelectArchetypeResources(m *cataloggen.Manifest, n int) []cataloggen.ResourceEntry {
	included := m.IncludedResources()
	sort.Slice(included, func(i, j int) bool { return included[i].ResourceKey < included[j].ResourceKey })
	if n > len(included) {
		n = len(included)
	}
	return included[:n]
}

// permission is a small constructor to keep GenerateArchetypeCapabilities
// readable.
func permission(resource, action, targetRef string) Expression {
	return Expression{Permission: &PermissionRequirement{Resource: resource, Action: action, TargetRef: targetRef}}
}

// GenerateArchetypeCapabilities instantiates the five PRD archetypes across
// resources, deterministically:
//
//	X.route.list    = allOf(X:list, hospital_context:read)
//	X.route.details = allOf(X:read, related:read)
//	X.route.edit    = allOf(X:read, X:update, related:list)
//	X.button.assign = allOf(X:read, X:assign)
//	X.action.delete = allOf(X:read, X:delete)
//
// "related" is the next resource in the (sorted, wrapping) list. This is
// what makes leaf sharing genuinely non-trivial rather than trivially
// satisfied (PRD): resource i's "related:read"/"related:list" leaf is the
// exact same leaf as resource i+1's own "X:read"/"X:list" leaf.
func GenerateArchetypeCapabilities(resources []cataloggen.ResourceEntry, catalogRevision int64) []UiCapabilityDefinition {
	n := len(resources)
	if n == 0 {
		return nil
	}

	var defs []UiCapabilityDefinition
	for i, r := range resources {
		related := resources[(i+1)%n]

		defs = append(defs,
			UiCapabilityDefinition{
				Key:             r.ResourceKey + ".route.list",
				Module:          r.Domain,
				Context:         "COLLECTION",
				CatalogRevision: catalogRevision,
				Expression: Expression{AllOf: []Expression{
					permission(r.ResourceKey, "list", r.ResourceKey+"Collection"),
					permission(HospitalContextResource, "read", "hospitalContext"),
				}},
			},
			UiCapabilityDefinition{
				Key:             r.ResourceKey + ".route.details",
				Module:          r.Domain,
				Context:         "INSTANCE",
				CatalogRevision: catalogRevision,
				Expression: Expression{AllOf: []Expression{
					permission(r.ResourceKey, "read", r.ResourceKey),
					permission(related.ResourceKey, "read", related.ResourceKey),
				}},
			},
			UiCapabilityDefinition{
				Key:             r.ResourceKey + ".route.edit",
				Module:          r.Domain,
				Context:         "INSTANCE",
				CatalogRevision: catalogRevision,
				Expression: Expression{AllOf: []Expression{
					permission(r.ResourceKey, "read", r.ResourceKey),
					permission(r.ResourceKey, "update", r.ResourceKey),
					permission(related.ResourceKey, "list", related.ResourceKey+"Collection"),
				}},
			},
			UiCapabilityDefinition{
				Key:             r.ResourceKey + ".button.assign",
				Module:          r.Domain,
				Context:         "INSTANCE",
				CatalogRevision: catalogRevision,
				Expression: Expression{AllOf: []Expression{
					permission(r.ResourceKey, "read", r.ResourceKey),
					permission(r.ResourceKey, "assign", r.ResourceKey),
				}},
			},
			UiCapabilityDefinition{
				Key:             r.ResourceKey + ".action.delete",
				Module:          r.Domain,
				Context:         "INSTANCE",
				CatalogRevision: catalogRevision,
				Expression: Expression{AllOf: []Expression{
					permission(r.ResourceKey, "read", r.ResourceKey),
					permission(r.ResourceKey, "delete", r.ResourceKey),
				}},
			},
		)
	}

	sort.Slice(defs, func(i, j int) bool { return defs[i].Key < defs[j].Key })
	return defs
}

// CountLeafOccurrences counts how many times each distinct
// (resource, action) permission leaf is used across defs, proving the
// "leaf sharing is measurable and non-trivial" acceptance criterion.
func CountLeafOccurrences(defs []UiCapabilityDefinition) map[string]int {
	counts := map[string]int{}
	for _, d := range defs {
		walkExpression(d.Expression, func(e Expression) {
			if e.Permission != nil {
				key := fmt.Sprintf("%s:%s", e.Permission.Resource, e.Permission.Action)
				counts[key]++
			}
		})
	}
	return counts
}
