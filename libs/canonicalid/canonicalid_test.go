package canonicalid_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/canonicalid"
)

// §7.5 fixes the three spellings. They are the join between a token and the
// role-permission matrix, so the formats are asserted literally: a change here
// silently resolves every principal to no permissions at all.
func TestTheCanonicalFormatsAreTheOnesSection75Specifies(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "a Keycloak realm role",
			got:  canonicalid.KeycloakRealmRole("tenant-a", "auditor"),
			want: "kc:tenant-a:realm:auditor",
		},
		{
			name: "a Keycloak client role",
			got:  canonicalid.KeycloakClientRole("tenant-a", "patient-app", "doctor"),
			want: "kc:tenant-a:patient-app:doctor",
		},
		{
			name: "a WSO2 role or group",
			got:  canonicalid.WSO2Role("carbon.super", "doctor"),
			want: "wso2:carbon.super:doctor",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Errorf("= %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestReservedRolesAreRecognisedWhateverTheirCase(t *testing.T) {
	reserved := []string{
		"sys:permission-evaluator",
		"sys:anything",
		"SYS:UPPERCASE",
		"Sys:Mixed",
	}
	for _, role := range reserved {
		if !canonicalid.IsReserved(role) {
			t.Errorf("IsReserved(%q) = false, want true", role)
		}
	}
}

func TestAnOrdinaryRoleIsNotReserved(t *testing.T) {
	ordinary := []string{
		"kc:tenant-a:patient-app:doctor",
		"kc:tenant-a:realm:auditor",
		"wso2:carbon.super:doctor",
		"doctor",
		"",
	}
	for _, role := range ordinary {
		if canonicalid.IsReserved(role) {
			t.Errorf("IsReserved(%q) = true, want false", role)
		}
	}
}
