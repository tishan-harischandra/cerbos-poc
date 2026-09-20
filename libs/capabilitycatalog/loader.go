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

// LoadDefinitionsDir parses the whole catalog under dir into a single,
// key-sorted slice of definitions, stamping each with its file's
// catalogRevision. Sorting keeps loading (and therefore downstream
// generation and seeding) deterministic regardless of directory iteration
// order.
//
// Both layouts are read: *.yaml directly under dir, and *.yaml one level
// down in a per-module directory. Whole-catalog callers are the generator,
// the seed and the validation gate, which genuinely need everything; a
// service serving one module wants LoadDefinitionsForModule instead.
func LoadDefinitionsDir(dir string) ([]UiCapabilityDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var all []UiCapabilityDefinition
	for _, entry := range entries {
		if entry.IsDir() {
			defs, err := LoadDefinitionsForModule(dir, entry.Name())
			if err != nil {
				return nil, err
			}
			all = append(all, defs...)
			continue
		}
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		defs, err := loadDefinitionFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		all = append(all, defs...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all, nil
}

// LoadDefinitionsForModule parses only the *.yaml files under dir/module
// into a key-sorted slice, stamping each with its file's catalogRevision.
//
// A module owns a directory rather than a single file because generated and
// hand-authored capabilities can belong to the same module - the committed
// catalog's `clinical` holds both - and because an adopter authoring a large
// module will want to split it.
//
// This exists so serving one module costs one module. LoadDefinitionsDir
// parses the whole tree, which is what ADR-013 measured at 528.6 MiB peak
// for 60,000 capabilities against a 512Mi limit: an OOMKill on the first
// capability request after a pod start, because the load is lazy.
//
// An unknown module is empty, not an error: the console may ask for a module
// this installation's adopter never authored.
func LoadDefinitionsForModule(dir, module string) ([]UiCapabilityDefinition, error) {
	moduleDir := filepath.Join(dir, module)
	entries, err := os.ReadDir(moduleDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", moduleDir, err)
	}

	var all []UiCapabilityDefinition
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(moduleDir, entry.Name())
		defs, err := loadDefinitionFile(path)
		if err != nil {
			return nil, err
		}
		all = append(all, defs...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all, nil
}

// loadDefinitionFile parses one definition file and stamps its capabilities
// with the file's catalogRevision.
func loadDefinitionFile(path string) ([]UiCapabilityDefinition, error) {
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
	return df.Capabilities, nil
}

// catalogResourceEntry is the shape of one file under
// deploy/cerbos/catalog/resources: the administration-facing metadata
// source (§6.1), which is also the "active resource catalog" permission
// leaves are validated against.
type catalogResourceEntry struct {
	Resource    string `yaml:"resource"`
	Version     string `yaml:"version"`
	DisplayName string `yaml:"displayName"`
	Domain      string `yaml:"domain"`
	Actions     []struct {
		Key         string `yaml:"key"`
		DisplayName string `yaml:"displayName"`
		Context     string `yaml:"context"`
		Risk        string `yaml:"risk"`
	} `yaml:"actions"`
}

// ActionEntry is one action a resource declares in the administration-facing
// catalog (§9.1's "Resource catalog" module, §9.2's "actions grouped by
// collection, instance and workflow context").
type ActionEntry struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Context     string `json:"context"`
	// Risk is STANDARD or ELEVATED (§6.1, issue #18's resource catalog
	// risk metadata), the same classification
	// libs/cataloggen.RenderActionSeedCSV assigns the DB seed's
	// risk_level column from the manifest's lockableActions list.
	Risk string `json:"risk"`
}

// ResourceEntry is one resource's full administration-facing catalog entry:
// everything the Admin Console's resource catalog and role matrix modules
// need to render and let an administrator search, without knowing the file
// format underneath.
type ResourceEntry struct {
	ResourceKey string        `json:"resourceKey"`
	Version     string        `json:"version"`
	DisplayName string        `json:"displayName"`
	Domain      string        `json:"domain"`
	Actions     []ActionEntry `json:"actions"`
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

// LoadResourceCatalogDir reads every *.yaml file directly under dir
// (deploy/cerbos/catalog/resources) into a key-sorted slice of
// ResourceEntry, the shape the Admin Console's resource catalog and role
// matrix modules serve to the browser (§9.1, §9.4's
// "GET /admin/authz/resources").
//
// Sorting keeps the response deterministic regardless of directory
// iteration order, the same reason LoadDefinitionsDir sorts its result.
func LoadResourceCatalogDir(dir string) ([]ResourceEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var all []ResourceEntry
	for _, file := range entries {
		if file.IsDir() || filepath.Ext(file.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, file.Name())
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
		actions := make([]ActionEntry, 0, len(e.Actions))
		for _, a := range e.Actions {
			actions = append(actions, ActionEntry{Key: a.Key, DisplayName: a.DisplayName, Context: a.Context, Risk: a.Risk})
		}
		all = append(all, ResourceEntry{
			ResourceKey: e.Resource, Version: e.Version,
			DisplayName: e.DisplayName, Domain: e.Domain, Actions: actions,
		})
	}

	sort.Slice(all, func(i, j int) bool { return all[i].ResourceKey < all[j].ResourceKey })
	return all, nil
}
