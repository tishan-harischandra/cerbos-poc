package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// ConcreteAdapterPackages are the vendor implementations behind this
// repository's provider-neutral ports. Importing one binds a consumer to a
// product: the type it returns, the errors it reports and the configuration it
// needs all become part of that consumer's contract, and the "swap the provider
// by changing one environment variable" promise quietly stops being true.
var ConcreteAdapterPackages = []string{
	// The identity directory adapters, selected by IDP_TYPE.
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/keycloak",
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/wso2",
	// The leader election adapters, selected by LEADER_ELECTION_TYPE
	// (ADR-009). A service that named one could not be moved between the
	// compose and Kubernetes deployments without a code change, which is
	// the whole reason the port exists.
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/pgadvisory",
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/databaselock",
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/k8slease",
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/redislock",
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/single",
}

// CompositionRoots are the only places allowed to name an adapter: each
// provider factory that selects one, and each adapter's own package.
var CompositionRoots = []string{
	"libs/idpdirectory/provider/",
	"libs/idpdirectory/keycloak/",
	"libs/idpdirectory/wso2/",
	"libs/leaderlock/provider/",
	"libs/leaderlock/pgadvisory/",
	"libs/leaderlock/databaselock/",
	"libs/leaderlock/k8slease/",
	"libs/leaderlock/redislock/",
	"libs/leaderlock/single/",
}

// ScanAdapterImports reports every import of a concrete adapter from a file
// that is not a composition root.
func ScanAdapterImports(filename, relativePath, src string) ([]Finding, error) {
	if IsCompositionRoot(relativePath) {
		return nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	var findings []Finding
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		for _, adapter := range ConcreteAdapterPackages {
			if path != adapter {
				continue
			}
			findings = append(findings, Finding{
				File:   filename,
				Line:   fset.Position(imported.Pos()).Line,
				Symbol: path,
				Message: "only the provider factory may name a concrete adapter; depend on the port " +
					"instead (idpdirectory.IdentityDirectory, leaderlock.Elector)",
			})
		}
	}
	return findings, nil
}

// IsCompositionRoot reports whether a path is allowed to name an adapter. Test
// files are included: a test asserting an adapter's own behaviour has to
// construct it.
func IsCompositionRoot(relativePath string) bool {
	normalised := strings.ReplaceAll(relativePath, "\\", "/")
	if strings.HasSuffix(normalised, "_test.go") {
		return true
	}
	for _, root := range CompositionRoots {
		if strings.HasPrefix(normalised, root) {
			return true
		}
	}
	return false
}
