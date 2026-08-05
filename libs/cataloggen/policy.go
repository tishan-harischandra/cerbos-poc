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
	b.WriteString("      isolated: >-\n")
	b.WriteString("        request.principal.attr.tenantId == request.resource.attr.tenantId &&\n")
	b.WriteString("        request.principal.attr.hospitalId == request.resource.attr.hospitalId\n")
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
	b.WriteString("          expr: \"!variables.isolated\"\n\n")

	if len(m.LockableActions) > 0 {
		b.WriteString("    - name: locked_record_restriction\n")
		fmt.Fprintf(&b, "      actions: %s\n", quotedList(m.LockableActions))
		b.WriteString("      effect: EFFECT_DENY\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		b.WriteString("          expr: variables.is_locked\n\n")
	}

	for _, action := range m.Actions {
		fmt.Fprintf(&b, "    - name: revoke_%s\n", action.Key)
		fmt.Fprintf(&b, "      actions: [%q]\n", action.Key)
		b.WriteString("      effect: EFFECT_DENY\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		fmt.Fprintf(&b, "          expr: '%q in variables.user_revokes'\n\n", action.Key)
	}

	for _, action := range m.Actions {
		fmt.Fprintf(&b, "    - name: grant_%s_to_user\n", action.Key)
		fmt.Fprintf(&b, "      actions: [%q]\n", action.Key)
		b.WriteString("      effect: EFFECT_ALLOW\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		fmt.Fprintf(&b, "          expr: '%q in variables.user_grants'\n\n", action.Key)
	}

	for i, action := range m.Actions {
		fmt.Fprintf(&b, "    - name: grant_%s_to_role\n", action.Key)
		fmt.Fprintf(&b, "      actions: [%q]\n", action.Key)
		b.WriteString("      effect: EFFECT_ALLOW\n")
		b.WriteString("      roles: [\"sys:permission-evaluator\"]\n")
		b.WriteString("      condition:\n")
		b.WriteString("        match:\n")
		fmt.Fprintf(&b, "          expr: '%q in variables.role_actions'\n", action.Key)
		if i < len(m.Actions)-1 {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n  schemas:\n")
	b.WriteString("    principalSchema:\n")
	b.WriteString("      ref: cerbos:///principal.json\n")
	b.WriteString("    resourceSchema:\n")
	fmt.Fprintf(&b, "      ref: cerbos:///%s.json\n", entry.ResourceKey)

	return b.String()
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
