package cataloggen

import (
	"fmt"
	"strings"
)

// generatedHeader is prepended to every generated file so a reviewer never
// mistakes generated output for a hand-authored file, and so the drift check
// in tests/architecture can recognise its own output.
const generatedHeader = "" +
	"# GENERATED FILE - DO NOT EDIT BY HAND.\n" +
	"#\n" +
	"# Produced by libs/cataloggen from libs/cataloggen/manifest.yaml" +
	" (catalog revision %d).\n" +
	"# Edit the manifest and re-run `make catalog-gen`, or regenerate directly\n" +
	"# with `go run ./libs/cataloggen/cmd/cataloggen -manifest libs/cataloggen/manifest.yaml`.\n"

// RenderPolicy renders the root Cerbos resource policy for one manifest
// entry, following the §6.5 / ADR-006 pattern already proven by
// patient_record.yaml: every rule binds to the single synthetic role
// `sys:permission-evaluator`, mandatory restrictions are explicit denies, and
// revoke/grant/role-grant is one rule per action because a Cerbos condition
// cannot name the action being evaluated.
func RenderPolicy(m *Manifest, entry ResourceEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, generatedHeader, m.CatalogRevision)
	b.WriteString("apiVersion: api.cerbos.dev/v1\n")
	b.WriteString("resourcePolicy:\n")
	fmt.Fprintf(&b, "  resource: %s\n", entry.ResourceKey)
	b.WriteString("  version: v1\n\n")

	b.WriteString("  variables:\n")
	b.WriteString("    local:\n")
	// issue #81: an empty hospital (a tenant-wide administrator session,
	// issue #80) satisfies isolation against any hospital in the same
	// tenant, because role_permission - what role_actions comes from -
	// carries no hospital dimension at all (§8.1): a role means the same
	// thing across the whole tenant, and tenant-wide is exactly "the
	// tenant's own role grants, no single hospital". hospital_scoped is
	// the opposite fact, used below to keep the directional half of the
	// invariant explicit at the one place it could otherwise leak: a user
	// grant is a hospital-narrowed assignment by construction (§8.1 gives
	// user_permission_override a hospital_id column precisely because it
	// does not mean the same thing everywhere), so it must never apply to
	// a session with no hospital, however isolated that session's tenant
	// isolation check reads.
	b.WriteString("      isolated: >-\n")
	b.WriteString("        request.principal.attr.tenantId == request.resource.attr.tenantId &&\n")
	b.WriteString("        (request.principal.attr.hospitalId == \"\" ||\n")
	b.WriteString("         request.principal.attr.hospitalId == request.resource.attr.hospitalId)\n")
	b.WriteString("      hospital_scoped: request.principal.attr.hospitalId != \"\"\n")
	b.WriteString("      role_actions: request.resource.attr.permissionContext.roleGrantedActions\n")
	b.WriteString("      user_grants: request.resource.attr.permissionContext.userGrantedActions\n")
	b.WriteString("      user_revokes: request.resource.attr.permissionContext.userRevokedActions\n")
	b.WriteString("      is_locked: request.resource.attr.status == \"LOCKED\"\n\n")

	b.WriteString("  rules:\n")
	b.WriteString("    - name: tenant_and_hospital_isolation\n")
	b.WriteString("      actions: [\"*\"]\n")
	b.WriteString("      effect: EFFECT_DENY\n")
	b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
	b.WriteString("      condition:\n")
	b.WriteString("        match:\n")
	b.WriteString("          expr: \"!variables.isolated\"\n")
	b.WriteString(ruleOutput())

	if len(m.LockableActions) > 0 {
		b.WriteString("    - name: locked_record_restriction\n")
		fmt.Fprintf(&b, "      actions: %s\n", quotedList(m.LockableActions))
		b.WriteString("      effect: EFFECT_DENY\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		b.WriteString("          expr: variables.is_locked\n")
		b.WriteString(ruleOutput())
	}

	for _, action := range m.Actions {
		fmt.Fprintf(&b, "    - name: revoke_%s\n", action.Key)
		fmt.Fprintf(&b, "      actions: [%q]\n", action.Key)
		b.WriteString("      effect: EFFECT_DENY\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		fmt.Fprintf(&b, "          expr: '%q in variables.user_revokes'\n", action.Key)
		b.WriteString(ruleOutput())
	}

	for _, action := range m.Actions {
		fmt.Fprintf(&b, "    - name: grant_%s_to_user\n", action.Key)
		fmt.Fprintf(&b, "      actions: [%q]\n", action.Key)
		b.WriteString("      effect: EFFECT_ALLOW\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		// issue #81: a user grant is a hospital-narrowed assignment
		// (§8.1's user_permission_override carries a hospital_id column
		// role_permission does not), so variables.hospital_scoped keeps it
		// from ever applying to a tenant-wide session - defense in depth
		// at the one layer that decides, even though the assignment
		// service's own query for a tenant-wide principal's hospital_id
		// (an empty string) already matches no override row.
		fmt.Fprintf(&b, "          expr: '%q in variables.user_grants && variables.hospital_scoped'\n", action.Key)
		b.WriteString(ruleOutput())
	}

	for _, action := range m.Actions {
		fmt.Fprintf(&b, "    - name: grant_%s_to_role\n", action.Key)
		fmt.Fprintf(&b, "      actions: [%q]\n", action.Key)
		b.WriteString("      effect: EFFECT_ALLOW\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		fmt.Fprintf(&b, "          expr: '%q in variables.role_actions'\n", action.Key)
		b.WriteString(ruleOutput())
	}

	b.WriteString("  schemas:\n")
	b.WriteString("    principalSchema:\n")
	b.WriteString("      ref: cerbos:///principal.json\n")
	b.WriteString("    resourceSchema:\n")
	fmt.Fprintf(&b, "      ref: cerbos:///%s.json\n", entry.ResourceKey)

	return b.String()
}

// ruleOutput renders the output block every rule carries so that a caller can
// report *why* the PDP reached a decision without any service re-deciding
// precedence: Cerbos reports which named rule fired (in the response's
// outputs, keyed by the rule's name), and the rule name alone - revoke_*,
// grant_*_to_user, grant_*_to_role, locked_record_restriction,
// tenant_and_hospital_isolation - already identifies the decision source
// (USER_REVOKE, USER_GRANT, ROLE or MANDATORY_RULE). The output's own value
// carries no information; only the fact that the rule activated does.
func ruleOutput() string {
	return "      output:\n" +
		"        when:\n" +
		"          ruleActivated: '\"fired\"'\n\n"
}

// quotedList renders a Go string slice as a YAML flow-style list of quoted
// scalars: ["update", "delete", "assign"].
func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
