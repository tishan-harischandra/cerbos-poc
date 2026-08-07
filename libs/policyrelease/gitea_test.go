package policyrelease_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func TestSelectTag_PicksHighestProtectedSemverMatchingPrefix(t *testing.T) {
	tags := []policyrelease.Tag{
		{Name: "root-v1.3.0", Commit: "aaa", Protected: true},
		{Name: "root-v1.4.0", Commit: "bbb", Protected: true},
		{Name: "root-v1.5.0-rc", Commit: "ccc", Protected: false},
		{Name: "unrelated-v9.9.9", Commit: "ddd", Protected: true},
	}

	selected, err := policyrelease.SelectTag(tags, "root-v")
	if err != nil {
		t.Fatalf("SelectTag: %v", err)
	}

	if selected.Name != "root-v1.4.0" {
		t.Fatalf("selected = %q, want root-v1.4.0", selected.Name)
	}
	if selected.Commit != "bbb" {
		t.Fatalf("selected commit = %q, want bbb", selected.Commit)
	}
}
