package authz

// Source labels *why* a decision came out the way it did, for the four
// categories Appendix A defines. It is derived from the Cerbos rule names
// cerbosclient.Result.FiredRules already reports - a fact the PDP computed
// while deciding - and never from the assembled permissionContext action
// sets, so labelling a decision is not a second implementation of precedence
// (§21).
type Source string

const (
	SourceRole       Source = "ROLE"
	SourceUserGrant  Source = "USER_GRANT"
	SourceUserRevoke Source = "USER_REVOKE"
	SourceMandatory  Source = "MANDATORY_RULE"
)

// DecisionSource labels one leaf's decision from the rule names that fired
// for its resource. allowed is the PDP's own verdict for this action; the
// fired rule names only say which named rule produced it.
//
// Every generated and hand-authored resource policy names its rules
// tenant_and_hospital_isolation, locked_record_restriction, revoke_<action>,
// grant_<action>_to_user and grant_<action>_to_role (§6.5/ADR-006), so the
// rule name alone identifies the category without reading any action set.
//
// Exported so the capability endpoint (issue #11) can label the same
// leaf-level facts without a second implementation of this mapping.
func DecisionSource(action string, allowed bool, firedRules []string) Source {
	fired := make(map[string]bool, len(firedRules))
	for _, name := range firedRules {
		fired[name] = true
	}

	if allowed {
		if fired["grant_"+action+"_to_user"] {
			return SourceUserGrant
		}
		return SourceRole
	}

	// Denies. Isolation applies to every requested action at once (its rule
	// binds actions: ["*"]), so its firing denies this leaf regardless of
	// which other rules also fired.
	if fired["tenant_and_hospital_isolation"] {
		return SourceMandatory
	}
	if fired["locked_record_restriction"] {
		return SourceMandatory
	}
	if fired["revoke_"+action] {
		return SourceUserRevoke
	}
	// No named rule fired at all: the default-deny path, still a mandatory
	// outcome in the sense that nothing granted the action.
	return SourceMandatory
}
