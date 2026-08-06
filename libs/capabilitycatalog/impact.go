package capabilitycatalog

import "sort"

// CapabilitiesReferencing returns every capability in defs whose
// expression references the resource:action leaf, at any depth of
// allOf/anyOf nesting, sorted by key for a deterministic response (issue
// #18, §9.1's capability impact module: "Selecting a resource-action
// lists every capability that depends on it").
//
// The result is never nil: a resource-action no capability references
// returns an empty, non-nil slice, so a caller serialising it to JSON
// gets [] rather than null - the difference between "checked and found
// none" and "not checked" (the same acceptance criterion's "clearly
// shown as such rather than as an empty error").
func CapabilitiesReferencing(defs []UiCapabilityDefinition, resource, action string) []UiCapabilityDefinition {
	matches := make([]UiCapabilityDefinition, 0)
	for _, d := range defs {
		found := false
		walkExpression(d.Expression, func(e Expression) {
			if e.Permission != nil && e.Permission.Resource == resource && e.Permission.Action == action {
				found = true
			}
		})
		if found {
			matches = append(matches, d)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Key < matches[j].Key })
	return matches
}
