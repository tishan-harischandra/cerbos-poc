package cataloggen

import (
	"fmt"
	"strings"
)

// isLockable reports whether action is subject to the shared
// status == "LOCKED" mandatory restriction.
func isLockable(m *Manifest, action string) bool {
	for _, l := range m.LockableActions {
		if l == action {
			return true
		}
	}
	return false
}

func actionKeys(m *Manifest) []string {
	keys := make([]string, len(m.Actions))
	for i, a := range m.Actions {
		keys[i] = a.Key
	}
	return keys
}

func yamlStringList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// permissionContextBlock renders a permissionContext attribute block. Passing
// the full action list into roleGranted/userGranted/userRevoked exercises
// every one of the six actions with the same precedence outcome per row,
// which is what the §19.1 matrix asks for: it does not vary by action.
func permissionContextBlock(indent string, roleGranted, userGranted, userRevoked []string, revision int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%spermissionContext:\n", indent)
	fmt.Fprintf(&b, "%s  roleGrantedActions: %s\n", indent, yamlStringList(roleGranted))
	fmt.Fprintf(&b, "%s  userGrantedActions: %s\n", indent, yamlStringList(userGranted))
	fmt.Fprintf(&b, "%s  userRevokedActions: %s\n", indent, yamlStringList(userRevoked))
	fmt.Fprintf(&b, "%s  permissionRevision: %d\n", indent, revision)
	return b.String()
}

func expectedActionsBlock(indent string, actions []string, effect string) string {
	var b strings.Builder
	for _, a := range actions {
		fmt.Fprintf(&b, "%s  %s: %s\n", indent, a, effect)
	}
	return b.String()
}

// RenderPrecedenceTest renders the §19.1 minimum policy test matrix for one
// manifest entry, generalising patient_record_test.yaml to every action the
// manifest declares plus tenant, hospital and default-deny invariants.
func RenderPrecedenceTest(m *Manifest, entry ResourceEntry) string {
	actions := actionKeys(m)
	var lockable, nonLockable []string
	for _, a := range actions {
		if isLockable(m, a) {
			lockable = append(lockable, a)
		} else {
			nonLockable = append(nonLockable, a)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, generatedHeader, m.CatalogRevision)
	fmt.Fprintf(&b, "name: %sPrecedence\n", pascalName(entry))
	b.WriteString("description: Mandatory deny > user REVOKE > user GRANT > role grant > default deny\n\n")

	b.WriteString("principals:\n")
	b.WriteString("  doctor:\n")
	b.WriteString("    id: idp-user-123\n")
	b.WriteString("    roles:\n")
	b.WriteString("      - \"sys:permission-evaluator\"\n")
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-a\n")
	b.WriteString("      hospitalId: hospital-1\n")
	b.WriteString("      idpRoles:\n")
	b.WriteString("        - \"kc:tenant-a:realm:doctor\"\n\n")

	b.WriteString("  doctor_of_other_tenant:\n")
	b.WriteString("    id: idp-user-999\n")
	b.WriteString("    roles:\n")
	b.WriteString("      - \"sys:permission-evaluator\"\n")
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-b\n")
	b.WriteString("      hospitalId: hospital-1\n")
	b.WriteString("      idpRoles:\n")
	b.WriteString("        - \"kc:tenant-a:realm:doctor\"\n\n")

	b.WriteString("  doctor_of_other_hospital:\n")
	b.WriteString("    id: idp-user-888\n")
	b.WriteString("    roles:\n")
	b.WriteString("      - \"sys:permission-evaluator\"\n")
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-a\n")
	b.WriteString("      hospitalId: hospital-2\n")
	b.WriteString("      idpRoles:\n")
	b.WriteString("        - \"kc:tenant-a:realm:doctor\"\n\n")

	// issue #81: an administrator's tenant-wide session (issue #80) - no
	// active hospital at all, never an empty string standing in for "every
	// hospital" by omission.
	b.WriteString("  administrator_tenant_wide:\n")
	b.WriteString("    id: idp-user-777\n")
	b.WriteString("    roles:\n")
	b.WriteString("      - \"sys:permission-evaluator\"\n")
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-a\n")
	b.WriteString("      hospitalId: \"\"\n")
	b.WriteString("      idpRoles:\n")
	b.WriteString("        - \"kc:tenant-a:realm:administrator\"\n\n")

	b.WriteString("resources:\n")

	writeResource := func(name, tenant, hospital, status string, role, userGrant, userRevoke []string) {
		fmt.Fprintf(&b, "  %s:\n", name)
		fmt.Fprintf(&b, "    kind: %s\n", entry.ResourceKey)
		b.WriteString("    policyVersion: v1\n")
		fmt.Fprintf(&b, "    id: %s-456\n", entry.ResourceKey)
		b.WriteString("    attr:\n")
		fmt.Fprintf(&b, "      tenantId: %s\n", tenant)
		fmt.Fprintf(&b, "      hospitalId: %s\n", hospital)
		fmt.Fprintf(&b, "      status: %s\n", status)
		b.WriteString(permissionContextBlock("      ", role, userGrant, userRevoke, 184))
		b.WriteString("\n")
	}

	writeResource("revoked_over_role_grant", "tenant-a", "hospital-1", "ACTIVE", actions, nil, actions)
	writeResource("revoked_without_role_grant", "tenant-a", "hospital-1", "ACTIVE", nil, nil, actions)
	writeResource("user_granted_without_role_grant", "tenant-a", "hospital-1", "ACTIVE", nil, actions, nil)
	writeResource("user_granted_with_role_grant", "tenant-a", "hospital-1", "ACTIVE", actions, actions, nil)
	writeResource("inherited_with_role_grant", "tenant-a", "hospital-1", "ACTIVE", actions, nil, nil)
	writeResource("inherited_without_role_grant", "tenant-a", "hospital-1", "ACTIVE", nil, nil, nil)
	writeResource("locked_with_every_grant", "tenant-a", "hospital-1", "LOCKED", actions, actions, nil)
	writeResource("other_tenant_record", "tenant-b", "hospital-1", "ACTIVE", actions, actions, nil)
	writeResource("other_hospital_record", "tenant-a", "hospital-2", "ACTIVE", actions, actions, nil)

	b.WriteString("tests:\n")

	writeCase := func(name, principal, resource string, checkedActions []string, effect string) {
		fmt.Fprintf(&b, "  - name: %s\n", name)
		b.WriteString("    input:\n")
		fmt.Fprintf(&b, "      principals: [%s]\n", principal)
		fmt.Fprintf(&b, "      resources: [%s]\n", resource)
		fmt.Fprintf(&b, "      actions: %s\n", yamlStringList(checkedActions))
		b.WriteString("    expected:\n")
		fmt.Fprintf(&b, "      - principal: %s\n", principal)
		fmt.Fprintf(&b, "        resource: %s\n", resource)
		b.WriteString("        actions:\n")
		b.WriteString(expectedActionsBlock("        ", checkedActions, effect))
		b.WriteString("\n")
	}

	writeCase("a user revoke denies even when a role grants the action",
		"doctor", "revoked_over_role_grant", actions, "EFFECT_DENY")
	writeCase("a user revoke denies when no role grants the action",
		"doctor", "revoked_without_role_grant", actions, "EFFECT_DENY")
	writeCase("a user grant allows when no role grants the action",
		"doctor", "user_granted_without_role_grant", actions, "EFFECT_ALLOW")
	writeCase("a user grant allows when a role also grants the action",
		"doctor", "user_granted_with_role_grant", actions, "EFFECT_ALLOW")
	writeCase("an inherited override falls through to the role grant",
		"doctor", "inherited_with_role_grant", actions, "EFFECT_ALLOW")
	writeCase("an inherited override with no role grant falls through to default deny",
		"doctor", "inherited_without_role_grant", actions, "EFFECT_DENY")

	if len(lockable) > 0 {
		writeCase("a locked record denies mutation regardless of role or user grant",
			"doctor", "locked_with_every_grant", lockable, "EFFECT_DENY")
	}
	if len(nonLockable) > 0 {
		writeCase("locking a record does not withdraw non-mutating actions",
			"doctor", "locked_with_every_grant", nonLockable, "EFFECT_ALLOW")
	}

	writeCase("a record in another tenant is denied every action",
		"doctor", "other_tenant_record", actions, "EFFECT_DENY")
	writeCase("a principal from another tenant is denied every action",
		"doctor_of_other_tenant", "inherited_with_role_grant", actions, "EFFECT_DENY")
	writeCase("a record in another hospital of the same tenant is denied every action",
		"doctor", "other_hospital_record", actions, "EFFECT_DENY")
	writeCase("a principal from another hospital is denied every action",
		"doctor_of_other_hospital", "inherited_with_role_grant", actions, "EFFECT_DENY")

	// issue #81: the tenant-wide case. A role grant carries no hospital
	// dimension (§8.1), so a tenant-wide session reaches it regardless of
	// which hospital the resource belongs to - "an empty hospital matches
	// tenant-wide assignments". A user grant is hospital-narrowed by
	// construction, so the same session must never reach it, whatever
	// hospital the resource happens to belong to - "and never a
	// hospital-narrowed one". Tenant isolation itself is not weakened:
	// a tenant-wide session still belongs to exactly one tenant.
	writeCase("a tenant-wide session reaches a role grant in any hospital of its tenant",
		"administrator_tenant_wide", "other_hospital_record", actions, "EFFECT_ALLOW")
	writeCase("a tenant-wide session cannot reach a hospital-narrowed user grant",
		"administrator_tenant_wide", "user_granted_without_role_grant", actions, "EFFECT_DENY")
	writeCase("a tenant-wide session is still denied outside its own tenant",
		"administrator_tenant_wide", "other_tenant_record", actions, "EFFECT_DENY")

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// RenderSchemaTest renders schema-enforcement tests for one manifest entry,
// generalising patient_record_schema_test.yaml: requests missing tenantId,
// hospitalId or permissionContext, or carrying attributes the schema does not
// declare, must be denied before rule evaluation ever runs (§19, [R10]).
func RenderSchemaTest(m *Manifest, entry ResourceEntry) string {
	actions := actionKeys(m)
	probe := actions[0]

	var b strings.Builder
	fmt.Fprintf(&b, generatedHeader, m.CatalogRevision)
	fmt.Fprintf(&b, "name: %sSchemaValidation\n", pascalName(entry))
	b.WriteString("description: Malformed authorization attributes are rejected before a decision\n\n")

	b.WriteString("principals:\n")
	b.WriteString("  doctor:\n")
	b.WriteString("    id: idp-user-123\n")
	b.WriteString("    roles:\n")
	b.WriteString("      - \"sys:permission-evaluator\"\n")
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-a\n")
	b.WriteString("      hospitalId: hospital-1\n")
	b.WriteString("      idpRoles:\n")
	b.WriteString("        - \"kc:tenant-a:realm:doctor\"\n\n")

	b.WriteString("  principal_without_tenant:\n")
	b.WriteString("    id: idp-user-124\n")
	b.WriteString("    roles:\n")
	b.WriteString("      - \"sys:permission-evaluator\"\n")
	b.WriteString("    attr:\n")
	b.WriteString("      hospitalId: hospital-1\n\n")

	b.WriteString("  principal_without_hospital:\n")
	b.WriteString("    id: idp-user-125\n")
	b.WriteString("    roles:\n")
	b.WriteString("      - \"sys:permission-evaluator\"\n")
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-a\n\n")

	b.WriteString("resources:\n")

	writeResource := func(name string, includeTenant, includeHospital, includePermissionContext bool, extraAttr string) {
		fmt.Fprintf(&b, "  %s:\n", name)
		fmt.Fprintf(&b, "    kind: %s\n", entry.ResourceKey)
		b.WriteString("    policyVersion: v1\n")
		fmt.Fprintf(&b, "    id: %s-456\n", entry.ResourceKey)
		b.WriteString("    attr:\n")
		if includeTenant {
			b.WriteString("      tenantId: tenant-a\n")
		}
		if includeHospital {
			b.WriteString("      hospitalId: hospital-1\n")
		}
		b.WriteString("      status: ACTIVE\n")
		if extraAttr != "" {
			fmt.Fprintf(&b, "      %s\n", extraAttr)
		}
		if includePermissionContext {
			b.WriteString(permissionContextBlock("      ", actions, actions, nil, 184))
		}
		b.WriteString("\n")
	}

	writeResource("fully_granted_record", true, true, true, "")
	writeResource("record_without_permission_context", true, true, false, "")
	writeResource("record_without_tenant", false, true, true, "")
	writeResource("record_without_hospital", true, false, true, "")

	fmt.Fprintf(&b, "  record_with_unknown_attribute:\n")
	fmt.Fprintf(&b, "    kind: %s\n", entry.ResourceKey)
	b.WriteString("    policyVersion: v1\n")
	fmt.Fprintf(&b, "    id: %s-456\n", entry.ResourceKey)
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-a\n")
	b.WriteString("      hospitalId: hospital-1\n")
	b.WriteString("      status: ACTIVE\n")
	b.WriteString("      ownerId: idp-user-123\n")
	b.WriteString(permissionContextBlock("      ", nil, nil, nil, 184))
	b.WriteString("\n")

	fmt.Fprintf(&b, "  record_with_unknown_status:\n")
	fmt.Fprintf(&b, "    kind: %s\n", entry.ResourceKey)
	b.WriteString("    policyVersion: v1\n")
	fmt.Fprintf(&b, "    id: %s-456\n", entry.ResourceKey)
	b.WriteString("    attr:\n")
	b.WriteString("      tenantId: tenant-a\n")
	b.WriteString("      hospitalId: hospital-1\n")
	b.WriteString("      status: SUPERSEDED\n")
	b.WriteString(permissionContextBlock("      ", nil, nil, nil, 184))

	b.WriteString("\ntests:\n")

	writeCase := func(name, principal, resource string) {
		fmt.Fprintf(&b, "  - name: %s\n", name)
		b.WriteString("    input:\n")
		fmt.Fprintf(&b, "      principals: [%s]\n", principal)
		fmt.Fprintf(&b, "      resources: [%s]\n", resource)
		fmt.Fprintf(&b, "      actions: [%q]\n", probe)
		b.WriteString("    expected:\n")
		fmt.Fprintf(&b, "      - principal: %s\n", principal)
		fmt.Fprintf(&b, "        resource: %s\n", resource)
		b.WriteString("        actions:\n")
		fmt.Fprintf(&b, "          %s: EFFECT_DENY\n", probe)
		b.WriteString("\n")
	}

	writeCase("a resource missing permissionContext is denied", "doctor", "record_without_permission_context")
	writeCase("a resource missing tenantId is denied", "doctor", "record_without_tenant")
	writeCase("a resource missing hospitalId is denied", "doctor", "record_without_hospital")
	writeCase("a principal missing tenantId is denied", "principal_without_tenant", "fully_granted_record")
	writeCase("a principal missing hospitalId is denied", "principal_without_hospital", "fully_granted_record")
	writeCase("a resource attribute the schema does not declare is denied", "doctor", "record_with_unknown_attribute")
	writeCase("a status outside the declared set is denied", "doctor", "record_with_unknown_status")

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// pascalName recovers a PascalCase identifier from the resource key for use
// in Cerbos test suite names (PatientRecordPrecedence-style).
func pascalName(entry ResourceEntry) string {
	return entry.FHIRType
}
