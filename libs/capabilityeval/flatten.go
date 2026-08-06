// Package capabilityeval is the pure, zero-I/O half of the §12.3 backend
// evaluation algorithm (issue #11): flattening capability expressions into
// permission leaves, deduplicating them, and composing allOf/anyOf over a
// leaf-decision map the caller already obtained from Cerbos.
//
// Nothing here calls Cerbos, a database or the network. Resolving a
// targetRef into a concrete resource, calling the PDP and caching are all
// the ADS's job (apps/ads/internal/capability); this package only takes
// already-resolved, plain Go values and computes with them, which is what
// makes it unit-testable without any infrastructure test double.
//
// It depends on libs/capabilitycatalog for the UiCapabilityDefinition and
// Expression shapes rather than duplicating them. That package's own type
// definitions do no I/O either; only its separate loader functions (which
// capabilityeval never calls) touch the filesystem.
package capabilityeval

import (
	"fmt"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

// ResourceRef identifies one concrete resource instance, already resolved
// server-side from a symbolic targetRef (§12.2, §12.3 step 2).
type ResourceRef struct {
	Kind string
	ID   string
}

// Leaf is one resource-action pair - the unit a Cerbos decision answers for
// and the unit this package deduplicates by (§12.3 step 4).
type Leaf struct {
	Resource ResourceRef
	Action   string
}

// Flatten walks every definition's expression tree and returns the unique
// set of leaves that must be checked, resolving each permission's symbolic
// targetRef through targets.
//
// targets is the already-resolved server-side mapping from targetRef to a
// concrete resource (§12.2: "targetRef must be resolved server-side");
// resolving it is I/O and happens before this function is called.
func Flatten(defs []capabilitycatalog.UiCapabilityDefinition, targets map[string]ResourceRef) ([]Leaf, error) {
	seen := make(map[Leaf]struct{})
	var leaves []Leaf

	for _, def := range defs {
		if err := flattenExpression(def.Expression, targets, seen, &leaves); err != nil {
			return nil, fmt.Errorf("capability %q: %w", def.Key, err)
		}
	}

	return leaves, nil
}

func flattenExpression(e capabilitycatalog.Expression, targets map[string]ResourceRef, seen map[Leaf]struct{}, leaves *[]Leaf) error {
	switch {
	case e.Permission != nil:
		ref, ok := targets[e.Permission.TargetRef]
		if !ok {
			return fmt.Errorf("targetRef %q has no resolved resource", e.Permission.TargetRef)
		}
		leaf := Leaf{Resource: ref, Action: e.Permission.Action}
		if _, dup := seen[leaf]; !dup {
			seen[leaf] = struct{}{}
			*leaves = append(*leaves, leaf)
		}
		return nil
	case e.AllOf != nil:
		for _, child := range e.AllOf {
			if err := flattenExpression(child, targets, seen, leaves); err != nil {
				return err
			}
		}
		return nil
	case e.AnyOf != nil:
		for _, child := range e.AnyOf {
			if err := flattenExpression(child, targets, seen, leaves); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("expression has no populated field")
	}
}
