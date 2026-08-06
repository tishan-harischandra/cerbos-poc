package capabilitycatalog

import (
	"fmt"
	"sort"
	"strings"
)

// ActiveCatalog is the set of (resource, action) pairs a permission leaf may
// legally reference (§12.2: "Every permission leaf must resolve to a known
// catalog resource and action").
type ActiveCatalog struct {
	actions map[string]map[string]struct{}
}

// NewActiveCatalog returns an empty catalog. Add every known
// (resource, action) pair before calling Validate.
func NewActiveCatalog() *ActiveCatalog {
	return &ActiveCatalog{actions: map[string]map[string]struct{}{}}
}

// Add registers one known resource-action pair.
func (c *ActiveCatalog) Add(resource, action string) {
	if c.actions[resource] == nil {
		c.actions[resource] = map[string]struct{}{}
	}
	c.actions[resource][action] = struct{}{}
}

// Has reports whether resource/action is a known catalog pair.
func (c *ActiveCatalog) Has(resource, action string) bool {
	acts, ok := c.actions[resource]
	if !ok {
		return false
	}
	_, ok = acts[action]
	return ok
}

// Validate checks defs against catalog and against each other, returning
// every violation found (rather than stopping at the first) so a CI run
// reports the whole picture in one pass.
func Validate(defs []UiCapabilityDefinition, catalog *ActiveCatalog) []error {
	var errs []error

	byKey := make(map[string]UiCapabilityDefinition, len(defs))
	for _, d := range defs {
		if _, dup := byKey[d.Key]; dup {
			errs = append(errs, fmt.Errorf("capability %q is declared more than once", d.Key))
			continue
		}
		byKey[d.Key] = d
	}

	refs := map[string][]string{}
	for _, d := range defs {
		walkExpression(d.Expression, func(e Expression) {
			switch {
			case e.Permission != nil:
				if !catalog.Has(e.Permission.Resource, e.Permission.Action) {
					errs = append(errs, fmt.Errorf(
						"capability %q: unknown resource/action %q:%q",
						d.Key, e.Permission.Resource, e.Permission.Action))
				}
			case e.CapabilityRef != "":
				refs[d.Key] = append(refs[d.Key], e.CapabilityRef)
				if _, ok := byKey[e.CapabilityRef]; !ok {
					errs = append(errs, fmt.Errorf(
						"capability %q: references unknown capability %q", d.Key, e.CapabilityRef))
				}
			}
		})
	}

	if cycle := findCycle(refs); cycle != nil {
		errs = append(errs, fmt.Errorf("circular capability reference: %s", strings.Join(cycle, " -> ")))
	}

	return errs
}

// walkExpression visits e and every expression nested under its allOf/anyOf,
// depth first.
func walkExpression(e Expression, visit func(Expression)) {
	visit(e)
	for _, child := range e.AllOf {
		walkExpression(child, visit)
	}
	for _, child := range e.AnyOf {
		walkExpression(child, visit)
	}
}

// findCycle runs a colored DFS over the capabilityRef graph and returns the
// first cycle found as a path of capability keys, or nil if the graph is
// acyclic. Iterating in sorted key order keeps the result deterministic.
func findCycle(refs map[string][]string) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string
	var cycle []string

	var dfs func(node string) bool
	dfs = func(node string) bool {
		color[node] = gray
		path = append(path, node)
		for _, next := range refs[node] {
			if color[next] == gray {
				start := indexOf(path, next)
				cycle = append(append([]string{}, path[start:]...), next)
				return true
			}
			if color[next] == white {
				if dfs(next) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		color[node] = black
		return false
	}

	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if color[k] == white {
			if dfs(k) {
				return cycle
			}
		}
	}
	return nil
}

func indexOf(path []string, target string) int {
	for i, v := range path {
		if v == target {
			return i
		}
	}
	return 0
}
