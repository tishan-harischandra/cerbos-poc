package permissioncontext_test

import (
	"encoding/json"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

func TestRoleGrantedActionsCollectEveryEnabledRolePermission(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		Revision: 184,
		RolePermissions: []permissioncontext.RolePermission{
			{Role: "doctor", Action: "read", Enabled: true},
			{Role: "doctor", Action: "update", Enabled: true},
			{Role: "auditor", Action: "list", Enabled: true},
		},
	})

	assertActions(t, "roleGrantedActions", []string{"list", "read", "update"}, ctx.RoleGrantedActions)
}

func TestDisabledRolePermissionsAreNotGranted(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		RolePermissions: []permissioncontext.RolePermission{
			{Role: "doctor", Action: "read", Enabled: true},
			{Role: "doctor", Action: "delete", Enabled: false},
		},
	})

	assertActions(t, "roleGrantedActions", []string{"read"}, ctx.RoleGrantedActions)
}

func TestAnActionGrantedBySeveralRolesIsListedOnce(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		RolePermissions: []permissioncontext.RolePermission{
			{Role: "doctor", Action: "read", Enabled: true},
			{Role: "nurse", Action: "read", Enabled: true},
			{Role: "auditor", Action: "read", Enabled: true},
		},
	})

	assertActions(t, "roleGrantedActions", []string{"read"}, ctx.RoleGrantedActions)
}

func TestUserOverridesAreSplitByState(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		UserOverrides: []permissioncontext.UserOverride{
			{Action: "read", State: permissioncontext.Grant},
			{Action: "update", State: permissioncontext.Revoke},
			{Action: "delete", State: permissioncontext.Inherit},
		},
	})

	assertActions(t, "userGrantedActions", []string{"read"}, ctx.UserGrantedActions)
	assertActions(t, "userRevokedActions", []string{"update"}, ctx.UserRevokedActions)
}

func TestAnInheritedOverrideContributesNothing(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		RolePermissions: []permissioncontext.RolePermission{
			{Role: "doctor", Action: "read", Enabled: true},
		},
		UserOverrides: []permissioncontext.UserOverride{
			{Action: "read", State: permissioncontext.Inherit},
		},
	})

	assertActions(t, "roleGrantedActions", []string{"read"}, ctx.RoleGrantedActions)
	assertActions(t, "userGrantedActions", nil, ctx.UserGrantedActions)
	assertActions(t, "userRevokedActions", nil, ctx.UserRevokedActions)
}

// The guarantee that keeps precedence out of Go: when a role grants an action
// and the user revokes it, both facts are reported. Resolving them is Cerbos'
// job, so this library must not drop either side.
func TestAConflictBetweenARoleGrantAndAUserRevokeIsReportedNotResolved(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		RolePermissions: []permissioncontext.RolePermission{
			{Role: "doctor", Action: "update", Enabled: true},
		},
		UserOverrides: []permissioncontext.UserOverride{
			{Action: "update", State: permissioncontext.Revoke},
		},
	})

	assertActions(t, "roleGrantedActions", []string{"update"}, ctx.RoleGrantedActions)
	assertActions(t, "userRevokedActions", []string{"update"}, ctx.UserRevokedActions)
}

func TestAConflictBetweenAUserGrantAndAUserRevokeIsReportedNotResolved(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		UserOverrides: []permissioncontext.UserOverride{
			{Action: "delete", State: permissioncontext.Grant},
			{Action: "delete", State: permissioncontext.Revoke},
		},
	})

	assertActions(t, "userGrantedActions", []string{"delete"}, ctx.UserGrantedActions)
	assertActions(t, "userRevokedActions", []string{"delete"}, ctx.UserRevokedActions)
}

func TestThePermissionRevisionIsCarriedThrough(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{Revision: 4711})

	if ctx.PermissionRevision != 4711 {
		t.Errorf("permissionRevision = %d, want %d", ctx.PermissionRevision, 4711)
	}
}

// Cerbos compares the assembled context against a JSON schema, and the ADS
// caches it, so the wire form has to be stable rather than merely correct.
func TestTheWireFormIsTheContractCerbosExpects(t *testing.T) {
	ctx := permissioncontext.Assemble(permissioncontext.Input{
		Revision: 184,
		RolePermissions: []permissioncontext.RolePermission{
			{Role: "doctor", Action: "read", Enabled: true},
		},
		UserOverrides: []permissioncontext.UserOverride{
			{Action: "update", State: permissioncontext.Revoke},
		},
	})

	encoded, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshalling the context: %v", err)
	}

	const want = `{"roleGrantedActions":["read"],"userGrantedActions":[],` +
		`"userRevokedActions":["update"],"permissionRevision":184}`
	if got := string(encoded); got != want {
		t.Errorf("wire form mismatch\n got: %s\nwant: %s", got, want)
	}
}

// Empty sets must encode as [] rather than null: the schema types them as
// arrays, and a null would fail validation at the PDP.
func TestEmptySetsEncodeAsEmptyArrays(t *testing.T) {
	encoded, err := json.Marshal(permissioncontext.Assemble(permissioncontext.Input{}))
	if err != nil {
		t.Fatalf("marshalling the context: %v", err)
	}

	const want = `{"roleGrantedActions":[],"userGrantedActions":[],` +
		`"userRevokedActions":[],"permissionRevision":0}`
	if got := string(encoded); got != want {
		t.Errorf("wire form mismatch\n got: %s\nwant: %s", got, want)
	}
}

func assertActions(t *testing.T, field string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s = %v, want %v", field, got, want)
		}
	}
}
