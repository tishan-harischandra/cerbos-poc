package capabilityeval_test

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
)

// referenceEvaluate is a second, independent implementation of the same
// allOf/anyOf boolean semantics, written directly against plain Go bools
// rather than against capabilitycatalog.Expression or capabilityeval's own
// recursion. The property test below cross-checks Evaluate's verdict
// against this reference for many randomly generated trees, so a bug in
// Evaluate's actual recursive walk cannot also be present in the oracle it
// is compared to.
type refNode struct {
	leaf     int // -1 when this node is not a leaf
	isAnyOf  bool
	children []refNode
}

func referenceEvaluate(n refNode, leafAllowed map[int]bool) bool {
	if n.leaf >= 0 {
		return leafAllowed[n.leaf]
	}
	if n.isAnyOf {
		for _, c := range n.children {
			if referenceEvaluate(c, leafAllowed) {
				return true
			}
		}
		return false
	}
	for _, c := range n.children {
		if !referenceEvaluate(c, leafAllowed) {
			return false
		}
	}
	return true
}

// randomTree generates a random allOf/anyOf/permission tree of bounded
// depth and width, allocating fresh, globally unique leaf IDs as it goes.
func randomTree(rng *rand.Rand, depth int, nextLeaf *int) refNode {
	if depth <= 0 || rng.Intn(3) == 0 {
		id := *nextLeaf
		*nextLeaf++
		return refNode{leaf: id}
	}

	width := 1 + rng.Intn(3)
	children := make([]refNode, width)
	for i := range children {
		children[i] = randomTree(rng, depth-1, nextLeaf)
	}
	return refNode{leaf: -1, isAnyOf: rng.Intn(2) == 0, children: children}
}

// toExpression converts a refNode into the real capabilitycatalog.Expression
// shape Evaluate consumes, resolving each leaf to its own dedicated
// resource so no two leaves collide.
func toExpression(n refNode) capabilitycatalog.Expression {
	if n.leaf >= 0 {
		targetRef := fmt.Sprintf("target-%d", n.leaf)
		return permission("resource", "act", targetRef)
	}
	children := make([]capabilitycatalog.Expression, len(n.children))
	for i, c := range n.children {
		children[i] = toExpression(c)
	}
	if n.isAnyOf {
		return capabilitycatalog.Expression{AnyOf: children}
	}
	return capabilitycatalog.Expression{AllOf: children}
}

func collectLeaves(n refNode, out map[int]bool) {
	if n.leaf >= 0 {
		out[n.leaf] = true
		return
	}
	for _, c := range n.children {
		collectLeaves(c, out)
	}
}

// TestEvaluateMatchesAnIndependentBooleanReferenceAcrossRandomTrees is the
// issue #11 acceptance criterion "Composition can never produce an allow
// absent from all required leaf decisions, asserted by property test": for
// many random allOf/anyOf/permission trees and random leaf decisions,
// Evaluate's verdict must agree with the reference boolean evaluator, so an
// allOf can never be allowed while one of its leaves is denied and an anyOf
// can never be denied while one of its leaves is allowed.
func TestEvaluateMatchesAnIndependentBooleanReferenceAcrossRandomTrees(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	const iterations = 500
	for i := 0; i < iterations; i++ {
		nextLeaf := 0
		tree := randomTree(rng, 4, &nextLeaf)

		leafIDs := make(map[int]bool)
		collectLeaves(tree, leafIDs)

		leafAllowed := make(map[int]bool, len(leafIDs))
		targets := make(map[string]capabilityeval.ResourceRef, len(leafIDs))
		decisions := make(map[capabilityeval.Leaf]capabilityeval.LeafOutcome, len(leafIDs))
		for id := range leafIDs {
			allowed := rng.Intn(2) == 1
			leafAllowed[id] = allowed

			targetRef := fmt.Sprintf("target-%d", id)
			ref := capabilityeval.ResourceRef{Kind: "resource", ID: fmt.Sprintf("r-%d", id)}
			targets[targetRef] = ref
			decisions[capabilityeval.Leaf{Resource: ref, Action: "act"}] = capabilityeval.LeafOutcome{Allowed: allowed}
		}

		defs := []capabilitycatalog.UiCapabilityDefinition{{Key: "under-test", Expression: toExpression(tree)}}

		outcomes, err := capabilityeval.Evaluate(defs, targets, decisions)
		if err != nil {
			t.Fatalf("iteration %d: Evaluate: %v", i, err)
		}

		want := referenceEvaluate(tree, leafAllowed)
		got := outcomes["under-test"].Allowed
		if got != want {
			t.Fatalf("iteration %d: Evaluate returned %v, reference evaluator says %v, for tree %+v with decisions %v",
				i, got, want, tree, leafAllowed)
		}
	}
}
