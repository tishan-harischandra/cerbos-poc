package capabilityeval

import (
	"fmt"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

// LeafOutcome is what the caller already learned about one leaf: the PDP's
// decision, plus why (Appendix A's ROLE/USER_GRANT/USER_REVOKE/
// MANDATORY_RULE vocabulary, or any other reason code the caller supplies -
// this package treats it as an opaque string).
type LeafOutcome struct {
	Allowed bool
	Reason  string
}

// ReasonRequiredPermissionDenied is the stable, audience-safe top-level
// reason code for a denied capability (§12.4's snapshot example). It says
// only that composition failed, never which leaf or why - that detail lives
// in FailedRequirements, which is filtered out of end-user responses.
const ReasonRequiredPermissionDenied = "REQUIRED_PERMISSION_DENIED"

// FailedRequirement is one denied leaf that caused a capability to fail,
// the administration-audience evidence from §12.4.
type FailedRequirement struct {
	Resource string
	Action   string
	Target   string
	Reason   string
}

// Outcome is one capability's evaluated decision, in the full
// administration shape. ForEndUser strips it to the audience-safe shape.
type Outcome struct {
	Allowed            bool
	Reason             string
	FailedRequirements []FailedRequirement
}

// EndUserOutcome is the audience-filtered shape §12.4 requires for
// end-user paths: a stable reason code only, never the requirement tree.
type EndUserOutcome struct {
	Allowed bool
	Reason  string
}

// ForEndUser strips an Outcome down to what an end-user response may
// carry. Full requirement trees must never appear on end-user paths
// (issue #11 acceptance criteria); this is the one place that boundary is
// enforced structurally rather than by caller discipline.
func (o Outcome) ForEndUser() EndUserOutcome {
	return EndUserOutcome{Allowed: o.Allowed, Reason: o.Reason}
}

// Evaluate composes every definition's expression over decisions, the
// leaf-decision map the caller already obtained from Cerbos (§12.3 steps
// 7-8). A leaf with no entry in decisions is treated as denied: a leaf the
// caller never checked must never be silently allowed.
func Evaluate(defs []capabilitycatalog.UiCapabilityDefinition, targets map[string]ResourceRef, decisions map[Leaf]LeafOutcome) (map[string]Outcome, error) {
	outcomes := make(map[string]Outcome, len(defs))
	for _, def := range defs {
		allowed, failed, err := evaluateExpression(def.Expression, targets, decisions)
		if err != nil {
			return nil, fmt.Errorf("capability %q: %w", def.Key, err)
		}
		outcome := Outcome{Allowed: allowed}
		if !allowed {
			outcome.Reason = ReasonRequiredPermissionDenied
			outcome.FailedRequirements = failed
		}
		outcomes[def.Key] = outcome
	}
	return outcomes, nil
}

// evaluateExpression returns whether the expression is satisfied and, when
// it is not, the leaves whose denial explains why. A branch that itself
// evaluates to allowed contributes no evidence, even if nested inside a
// failing parent, and even if some of its own children were individually
// denied (§12.4: evidence should explain the failure, not list irrelevant
// detail from branches that passed).
func evaluateExpression(e capabilitycatalog.Expression, targets map[string]ResourceRef, decisions map[Leaf]LeafOutcome) (bool, []FailedRequirement, error) {
	switch {
	case e.Permission != nil:
		ref, ok := targets[e.Permission.TargetRef]
		if !ok {
			return false, nil, fmt.Errorf("targetRef %q has no resolved resource", e.Permission.TargetRef)
		}
		leaf := Leaf{Resource: ref, Action: e.Permission.Action}
		decision := decisions[leaf] // zero value: Allowed=false, unchecked leaves stay denied.
		if decision.Allowed {
			return true, nil, nil
		}
		return false, []FailedRequirement{{
			Resource: ref.Kind,
			Action:   e.Permission.Action,
			Target:   ref.ID,
			Reason:   decision.Reason,
		}}, nil

	case e.AllOf != nil:
		allowed := true
		var failed []FailedRequirement
		for _, child := range e.AllOf {
			childAllowed, childFailed, err := evaluateExpression(child, targets, decisions)
			if err != nil {
				return false, nil, err
			}
			if !childAllowed {
				allowed = false
				failed = append(failed, childFailed...)
			}
		}
		if allowed {
			return true, nil, nil
		}
		return false, failed, nil

	case e.AnyOf != nil:
		var failed []FailedRequirement
		for _, child := range e.AnyOf {
			childAllowed, childFailed, err := evaluateExpression(child, targets, decisions)
			if err != nil {
				return false, nil, err
			}
			if childAllowed {
				return true, nil, nil
			}
			failed = append(failed, childFailed...)
		}
		return false, failed, nil

	default:
		return false, nil, fmt.Errorf("expression has no populated field")
	}
}
