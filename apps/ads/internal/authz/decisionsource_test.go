package authz

import "testing"

func TestDecisionSourceLabelsEveryCategory(t *testing.T) {
	cases := []struct {
		name       string
		action     string
		allowed    bool
		firedRules []string
		want       Source
	}{
		{
			name:       "a role grant",
			action:     "read",
			allowed:    true,
			firedRules: []string{"grant_read_to_role"},
			want:       SourceRole,
		},
		{
			name:       "a user grant",
			action:     "read",
			allowed:    true,
			firedRules: []string{"grant_read_to_role", "grant_read_to_user"},
			want:       SourceUserGrant,
		},
		{
			name:       "a user revoke",
			action:     "update",
			allowed:    false,
			firedRules: []string{"revoke_update"},
			want:       SourceUserRevoke,
		},
		{
			name:       "tenant or hospital isolation",
			action:     "read",
			allowed:    false,
			firedRules: []string{"tenant_and_hospital_isolation"},
			want:       SourceMandatory,
		},
		{
			name:       "a locked record",
			action:     "update",
			allowed:    false,
			firedRules: []string{"locked_record_restriction"},
			want:       SourceMandatory,
		},
		{
			name:       "default deny with no rule fired",
			action:     "delete",
			allowed:    false,
			firedRules: nil,
			want:       SourceMandatory,
		},
		{
			name:       "isolation denies even alongside an unrelated revoke",
			action:     "read",
			allowed:    false,
			firedRules: []string{"tenant_and_hospital_isolation", "revoke_update"},
			want:       SourceMandatory,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DecisionSource(c.action, c.allowed, c.firedRules); got != c.want {
				t.Errorf("DecisionSource(%q, %v, %v) = %q, want %q",
					c.action, c.allowed, c.firedRules, got, c.want)
			}
		})
	}
}
