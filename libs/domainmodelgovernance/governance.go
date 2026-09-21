// Package domainmodelgovernance decides whether a domain-model activation
// may proceed, and describes the record that must survive it (ADR-013
// slice 5, issue #107).
//
// Changing the resource model used to be a reviewed commit to this
// repository. Once it is adopter data that a syncer activates
// automatically it becomes a privileged runtime operation, and ADR-013
// books the gap plainly: it "is a governance boundary this platform has no
// maker-checker for (DEVIATIONS N3) and no audit row for, since policy
// releases are not audited at all". ADR-014 widens it slightly - cloning a
// ref and pinning its SHA keeps content immutable but drops the
// tag-protection rights that decided who could mint a release. A commit SHA
// proves what activated, never who was entitled to activate it.
//
// Decide is a pure function of its arguments - no clock, no store, no
// network - so every row of the table below is testable in memory, the same
// property apps/keycloak-org-selector's OrganizationSelectionDecision was
// built for.
package domainmodelgovernance

import (
	"strings"
	"time"
)

// Scope is what an activation changes. The two are governed differently
// because their blast radius differs: the resource model generates the
// Cerbos policies themselves, while a capability catalog is a rendering
// hint the PEP enforces regardless (§12.5, DEVIATIONS S5).
type Scope string

const (
	// ScopeResourceModel is a change to the manifest that generates the
	// policy tree.
	ScopeResourceModel Scope = "RESOURCE_MODEL"
	// ScopeCapabilityCatalog is a change to the UI capability catalog only.
	ScopeCapabilityCatalog Scope = "CAPABILITY_CATALOG"
)

// State is where an approval request has got to. An approval is a
// one-way transition out of pending, so a refusal cannot be overturned and
// an approval cannot be replayed without a new request.
type State string

const (
	StatePending  State = "PENDING"
	StateApproved State = "APPROVED"
	StateRefused  State = "REFUSED"
)

// Reasons a decision came out the way it did. They are values rather than
// prose so an audit row and an API response can both carry the same one.
const (
	ReasonApproved       = "APPROVED_BY_SECOND_PERSON"
	ReasonSelfApproval   = "REFUSED_SELF_APPROVAL"
	ReasonNotPending     = "REFUSED_NOT_PENDING"
	ReasonUnknownActor   = "REFUSED_UNKNOWN_ACTOR"
	ReasonBypassed       = "APPROVAL_NOT_REQUIRED_FOR_SCOPE"
	ReasonUnknownScope   = "REFUSED_UNKNOWN_SCOPE"
)

// Policy is the installation's configured governance.
type Policy struct {
	// CapabilityChangesBypassApproval lets ScopeCapabilityCatalog activate
	// without a second person. Off by default: the safe value is the one an
	// installation gets by not thinking about it. It does not and must not
	// affect ScopeResourceModel.
	CapabilityChangesBypassApproval bool
}

// ApprovalRequest is one proposed activation awaiting a second person.
type ApprovalRequest struct {
	RequestID   string
	Scope       Scope
	Repository  string
	CommitSHA   string
	PreviousSHA string
	RequestedBy string
	RequestedAt time.Time
	State       State
}

// Decision is the outcome, and the reason, which is recorded either way -
// a refusal that leaves no trace is the case this package exists to
// prevent.
type Decision struct {
	Approved bool
	Reason   string
}

// Decide reports whether approver may activate request under policy.
//
// The order of the checks matters. State is tested before identity so a
// decided request cannot be re-decided by anyone, and the scope bypass is
// tested before identity so it can waive the second person without waiving
// the state machine.
func Decide(request ApprovalRequest, approver string, policy Policy) Decision {
	switch request.Scope {
	case ScopeResourceModel, ScopeCapabilityCatalog:
	default:
		return Decision{Reason: ReasonUnknownScope}
	}

	if request.State != StatePending {
		return Decision{Reason: ReasonNotPending}
	}

	if policy.CapabilityChangesBypassApproval && request.Scope == ScopeCapabilityCatalog {
		return Decision{Approved: true, Reason: ReasonBypassed}
	}

	requester := normaliseActor(request.RequestedBy)
	checker := normaliseActor(approver)

	// An anonymous party is not a second person. Without this, "" != "alice"
	// would let an unauthenticated caller satisfy the boundary.
	if requester == "" || checker == "" {
		return Decision{Reason: ReasonUnknownActor}
	}

	if requester == checker {
		return Decision{Reason: ReasonSelfApproval}
	}

	return Decision{Approved: true, Reason: ReasonApproved}
}

// normaliseActor makes identity comparison insensitive to case and
// surrounding space, so "Alice" cannot approve alice's own request.
func normaliseActor(actor string) string {
	return strings.ToLower(strings.TrimSpace(actor))
}
