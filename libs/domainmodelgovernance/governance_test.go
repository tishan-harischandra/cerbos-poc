package domainmodelgovernance_test

import (
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/domainmodelgovernance"
)

func request(requester string) domainmodelgovernance.ApprovalRequest {
	return domainmodelgovernance.ApprovalRequest{
		RequestID:   "req-1",
		Scope:       domainmodelgovernance.ScopeResourceModel,
		Repository:  "https://git.example/adopter/resources",
		CommitSHA:   "a1b2c3d4",
		PreviousSHA: "0000dead",
		RequestedBy: requester,
		RequestedAt: time.Unix(1_700_000_000, 0).UTC(),
		State:       domainmodelgovernance.StatePending,
	}
}

// DEVIATIONS N3: changing the resource model is a privileged runtime
// operation with no maker-checker. The whole point of this decision is that
// the person who asked cannot be the person who agreed.
func TestAnApproverMayNotApproveTheirOwnRequest(t *testing.T) {
	decision := domainmodelgovernance.Decide(request("alice"), "alice", domainmodelgovernance.Policy{})

	if decision.Approved {
		t.Fatal("a requester approved their own resource-model change")
	}
	if decision.Reason != domainmodelgovernance.ReasonSelfApproval {
		t.Errorf("reason = %q, want %q", decision.Reason, domainmodelgovernance.ReasonSelfApproval)
	}
}

func TestADifferentApproverMayApprove(t *testing.T) {
	decision := domainmodelgovernance.Decide(request("alice"), "bob", domainmodelgovernance.Policy{})

	if !decision.Approved {
		t.Fatalf("a distinct approver was refused: %q", decision.Reason)
	}
}

// Identity comparison must not be defeated by case or padding, or
// "Alice" approves alice's request.
func TestSelfApprovalIsDetectedRegardlessOfCaseOrSurroundingSpace(t *testing.T) {
	for _, approver := range []string{"Alice", "ALICE", "  alice  ", "\talice\n"} {
		decision := domainmodelgovernance.Decide(request("alice"), approver, domainmodelgovernance.Policy{})
		if decision.Approved {
			t.Errorf("approver %q approved alice's own request", approver)
		}
	}
}

// An anonymous approver is not a second person. Empty on either side must
// never satisfy the boundary, even though "" != "alice".
func TestAnEmptyIdentityNeverSatisfiesTheBoundary(t *testing.T) {
	if d := domainmodelgovernance.Decide(request("alice"), "", domainmodelgovernance.Policy{}); d.Approved {
		t.Error("an empty approver was accepted")
	}
	if d := domainmodelgovernance.Decide(request(""), "bob", domainmodelgovernance.Policy{}); d.Approved {
		t.Error("a request with no requester was approved")
	}
}

// Only a pending request can be approved: re-approving a decided request
// would let a refusal be overturned without a new request, and let an
// approval be replayed.
func TestOnlyAPendingRequestCanBeApproved(t *testing.T) {
	for _, state := range []domainmodelgovernance.State{
		domainmodelgovernance.StateApproved,
		domainmodelgovernance.StateRefused,
	} {
		req := request("alice")
		req.State = state

		decision := domainmodelgovernance.Decide(req, "bob", domainmodelgovernance.Policy{})
		if decision.Approved {
			t.Errorf("a %s request was approved again", state)
		}
		if decision.Reason != domainmodelgovernance.ReasonNotPending {
			t.Errorf("state %s: reason = %q, want %q", state, decision.Reason,
				domainmodelgovernance.ReasonNotPending)
		}
	}
}

// §12.5 has the PEP enforce regardless of the capability snapshot, and
// DEVIATIONS S5 already treats capability lag as benign, so an installation
// may let capability-only content activate without a second person. The
// resource model - which generates the policies themselves - may not.
func TestCapabilityOnlyActivationMayBypassApprovalWhenConfigured(t *testing.T) {
	policy := domainmodelgovernance.Policy{CapabilityChangesBypassApproval: true}

	req := request("alice")
	req.Scope = domainmodelgovernance.ScopeCapabilityCatalog

	decision := domainmodelgovernance.Decide(req, "alice", policy)
	if !decision.Approved {
		t.Fatalf("a capability-only change was refused under a bypass policy: %q", decision.Reason)
	}
	if decision.Reason != domainmodelgovernance.ReasonBypassed {
		t.Errorf("reason = %q, want %q", decision.Reason, domainmodelgovernance.ReasonBypassed)
	}
}

// The bypass is scoped. It must not leak to the resource model, which is
// the thing the boundary exists to protect.
func TestTheCapabilityBypassDoesNotCoverTheResourceModel(t *testing.T) {
	policy := domainmodelgovernance.Policy{CapabilityChangesBypassApproval: true}

	decision := domainmodelgovernance.Decide(request("alice"), "alice", policy)
	if decision.Approved {
		t.Fatal("the capability bypass approved a resource-model change")
	}
	if decision.Reason != domainmodelgovernance.ReasonSelfApproval {
		t.Errorf("reason = %q, want %q", decision.Reason, domainmodelgovernance.ReasonSelfApproval)
	}
}

// Without the bypass configured, capability changes are held to the same
// boundary - the default is the safe one.
func TestCapabilityChangesNeedApprovalByDefault(t *testing.T) {
	req := request("alice")
	req.Scope = domainmodelgovernance.ScopeCapabilityCatalog

	if d := domainmodelgovernance.Decide(req, "alice", domainmodelgovernance.Policy{}); d.Approved {
		t.Fatal("a capability change self-approved with no bypass configured")
	}
}
