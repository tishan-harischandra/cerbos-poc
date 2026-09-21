// The canonical role identifier drift gate (§7.5, issue #110).
//
// A canonical role identifier names where the role comes from:
// `kc:<realm>:realm:<role>` for a realm role, `kc:<realm>:<client>:<role>`
// for a client role (libs/canonicalid). Scripts and seeds hardcode these
// strings, and nothing checked them against the realms this repository
// actually seeds.
//
// That cost four months of red CI. Commit d7c92c8 moved the seeded roles
// from client roles on `patient-app` to realm roles; `patient-app` stopped
// declaring any client roles at all, and four hardcoded identifiers went on
// naming roles that no longer existed. The smoke suite reported it as
// `jq: Cannot iterate over null`, which says nothing about the cause.
//
// This gate reads the committed realm configuration and fails on any
// identifier that does not resolve in it, naming the file and the reason.
package architecture

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// SeededRealmDir holds the realm configurations Keycloak imports at start.
const SeededRealmDir = "deploy/keycloak"

// roleIdentifierPattern matches `kc:<realm>:<source>:<role>`. The role
// segment allows colons because `sys:forged-evaluator` contains one, and
// stops at whitespace or any delimiter a shell, Go or JSON string might
// close with - including the `\` of an escape sequence, since these
// identifiers appear inside generated string literals.
var roleIdentifierPattern = regexp.MustCompile(
	`kc:([a-z0-9-]+):([a-zA-Z0-9_-]+):([^\s"'` + "`" + `)}\[\],;\\]+)`)

// RoleIdentifier is one hardcoded canonical role identifier.
type RoleIdentifier struct {
	File  string
	Line  int
	Realm string
	// Source is "realm" for a realm role, or a client id for a client role.
	Source string
	Role   string
}

func (r RoleIdentifier) String() string {
	return fmt.Sprintf("kc:%s:%s:%s", r.Realm, r.Source, r.Role)
}

// SeededRealm is the part of a realm configuration this gate reads.
type SeededRealm struct {
	Realm string `json:"realm"`
	Roles struct {
		Realm  []struct{ Name string } `json:"realm"`
		Client map[string][]struct {
			Name string
		} `json:"client"`
	} `json:"roles"`
}

// LoadSeededRealm parses one realm configuration file.
func LoadSeededRealm(path string) (*SeededRealm, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var realm SeededRealm
	if err := json.Unmarshal(raw, &realm); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &realm, nil
}

// HasRole reports whether the realm declares role under source, where
// source is "realm" or a client id.
func (r *SeededRealm) HasRole(source, role string) bool {
	if source == "realm" {
		for _, declared := range r.Roles.Realm {
			if declared.Name == role {
				return true
			}
		}
		return false
	}
	for _, declared := range r.Roles.Client[source] {
		if declared.Name == role {
			return true
		}
	}
	return false
}

// ClientsWithRoles lists the client ids this realm declares client roles
// for, sorted, so a failure can say what the alternatives were.
func (r *SeededRealm) ClientsWithRoles() []string {
	var out []string
	for client, roles := range r.Roles.Client {
		if len(roles) > 0 {
			out = append(out, client)
		}
	}
	sort.Strings(out)
	return out
}

// ScanForRoleIdentifiers reports every hardcoded canonical role identifier
// in source.
func ScanForRoleIdentifiers(relativePath, source string) []RoleIdentifier {
	var found []RoleIdentifier
	for i, line := range strings.Split(source, "\n") {
		for _, match := range roleIdentifierPattern.FindAllStringSubmatch(line, -1) {
			found = append(found, RoleIdentifier{
				File:   relativePath,
				Line:   i + 1,
				Realm:  match[1],
				Source: match[2],
				Role:   match[3],
			})
		}
	}
	return found
}
