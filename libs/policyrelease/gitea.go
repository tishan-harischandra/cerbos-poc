// Package policyrelease implements the root policy release lifecycle (§13):
// polling Gitea for a protected root policy tag, validating the referenced
// commit, packaging it into an immutable archive, installing it atomically on
// every Cerbos replica through the pod-local Admin API, and only marking the
// release active once every healthy replica reports the target revision.
//
// Dynamic role and user assignment changes never pass through this package:
// nothing here touches assignment data, and nothing here computes a
// permission verdict (§6.4, ADR-003 - that belongs to Cerbos policy alone).
package policyrelease

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// Tag is one Git tag as reported by Gitea's tag listing API.
type Tag struct {
	// Name is the tag name, e.g. "root-v1.4.0".
	Name string
	// Commit is the exact commit SHA the tag points at. The validation gate
	// fetches this commit, never a moving branch head (§13.1).
	Commit string
	// Protected reports whether Gitea enforces this tag as protected.
	// Production installations pin a protected tag only (§13.1).
	Protected bool
}

// SelectTag picks the highest semantic version among protected tags whose
// name starts with prefix. An unprotected tag is never eligible: pinning an
// unprotected tag would let its target commit move underneath an already
// validated release.
func SelectTag(tags []Tag, prefix string) (Tag, error) {
	var candidates []Tag
	for _, t := range tags {
		if !t.Protected {
			continue
		}
		if !strings.HasPrefix(t.Name, prefix) {
			continue
		}
		version := "v" + strings.TrimPrefix(t.Name, prefix)
		if !semver.IsValid(version) {
			continue
		}
		candidates = append(candidates, t)
	}
	if len(candidates) == 0 {
		return Tag{}, fmt.Errorf("policyrelease: no protected tag matching prefix %q", prefix)
	}

	sort.Slice(candidates, func(i, j int) bool {
		vi := "v" + strings.TrimPrefix(candidates[i].Name, prefix)
		vj := "v" + strings.TrimPrefix(candidates[j].Name, prefix)
		return semver.Compare(vi, vj) < 0
	})

	return candidates[len(candidates)-1], nil
}
