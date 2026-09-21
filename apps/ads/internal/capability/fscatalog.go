package capability

import (
	"context"
	"fmt"

	"github.com/tishan-harischandra/cerbos-poc/libs/authzcache"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

// DefaultCatalogCacheSize bounds how many modules' definitions FSCatalog
// keeps warm at once. The installation's module count is small and known
// ahead of time (§9.1 lists eight admin console modules plus the business
// UI), so this is generous headroom rather than a tight tuning knob.
const DefaultCatalogCacheSize = 64

// FSCatalog serves UiCapabilityDefinitions from the same local directory
// tree Cerbos itself reads its policies from (§13: an immutable root-policy
// release is installed to a local path by a per-pod agent; this reads the
// capability catalog committed alongside it in
// deploy/cerbos/catalog/ui-capabilities). It caches per module through
// authzcache so a warm request never touches disk (§11.2, §15.1).
type FSCatalog struct {
	dir             string
	catalogRevision int64
	cache           *authzcache.Cache[string, []capabilitycatalog.UiCapabilityDefinition]
}

// NewFSCatalog builds an FSCatalog reading definitions from dir.
// catalogRevision is the release's numeric revision, formatted into the
// §12.4 snapshot shape ("ui-capabilities-v12").
func NewFSCatalog(dir string, catalogRevision int64, cache *authzcache.Cache[string, []capabilitycatalog.UiCapabilityDefinition]) *FSCatalog {
	if cache == nil {
		cache = authzcache.New[string, []capabilitycatalog.UiCapabilityDefinition](DefaultCatalogCacheSize, 0)
	}
	return &FSCatalog{dir: dir, catalogRevision: catalogRevision, cache: cache}
}

// Definitions returns every UiCapabilityDefinition belonging to module.
//
// It reads that module's directory and nothing else. Loading the whole
// catalog to answer for one module is what ADR-013 measured at 528.6 MiB
// peak for 60,000 capabilities against the 512Mi limit this deployment
// sets, and because the load is lazy the cost lands on the first capability
// request after a pod start rather than at boot.
func (c *FSCatalog) Definitions(_ context.Context, module string) ([]capabilitycatalog.UiCapabilityDefinition, string, error) {
	revision := formatCatalogRevision(c.catalogRevision)

	if cached, ok := c.cache.Get(module); ok {
		return cached, revision, nil
	}

	defs, err := capabilitycatalog.LoadDefinitionsForModule(c.dir, module)
	if err != nil {
		return nil, "", fmt.Errorf("loading capability module %s from %s: %w", module, c.dir, err)
	}

	c.cache.Set(module, defs)
	return defs, revision, nil
}
