package capabilitycatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// definitionFile is the on-disk shape of one capability definition file: a
// catalog revision shared by every capability it declares, plus the
// capabilities themselves (§12.2, §6.1).
type definitionFile struct {
	CatalogRevision int64                    `yaml:"catalogRevision"`
	Capabilities    []UiCapabilityDefinition `yaml:"capabilities"`
}

// LoadDefinitionsDir parses every *.yaml file directly under dir into a
// single, key-sorted slice of definitions, stamping each with its file's
// catalogRevision. Sorting keeps loading (and therefore downstream
// generation and seeding) deterministic regardless of directory iteration
// order.
func LoadDefinitionsDir(dir string) ([]UiCapabilityDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var all []UiCapabilityDefinition
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		var df definitionFile
		if err := yaml.Unmarshal(raw, &df); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		for i := range df.Capabilities {
			df.Capabilities[i].CatalogRevision = df.CatalogRevision
		}
		all = append(all, df.Capabilities...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all, nil
}

// catalogResourceEntry is the shape of one file under
// deploy/cerbos/catalog/resources: the administration-facing metadata
// source (§6.1), which is also the "active resource catalog" permission
// leaves are validated against.
type catalogResourceEntry struct {
	Resource string `yaml:"resource"`
	Actions  []struct {
		Key string `yaml:"key"`
	} `yaml:"actions"`
}

// LoadActiveCatalogDir builds an ActiveCatalog from every *.yaml file
// directly under dir (deploy/cerbos/catalog/resources), the single source
// of truth for which resource-action pairs a capability leaf may reference.
func LoadActiveCatalogDir(dir string) (*ActiveCatalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	catalog := NewActiveCatalog()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		var e catalogResourceEntry
		if err := yaml.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		if e.Resource == "" {
			return nil, fmt.Errorf("%s: catalog entry has no resource key", path)
		}
		for _, a := range e.Actions {
			catalog.Add(e.Resource, a.Key)
		}
	}

	return catalog, nil
}
