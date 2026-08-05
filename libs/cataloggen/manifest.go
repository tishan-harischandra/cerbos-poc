// Package cataloggen turns the committed FHIR resource manifest
// (manifest.yaml) into every downstream authorization artifact: the
// resource-action catalog, one Cerbos resource policy per resource, the
// principal and per-resource JSON schemas, the exhaustive Cerbos test suite
// and the database catalog seed (issue #8, §6.1, §6.5, §8, §19.1, §21).
//
// The manifest is the only hand-edited input. Everything this package emits
// is generated, golden-file tested and reviewed as a diff when the manifest
// or the generator changes.
package cataloggen

import (
	"embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed manifest.yaml
var embeddedManifest embed.FS

// Action is one of the six actions every resource exposes.
type Action struct {
	Key         string `yaml:"key"`
	DisplayName string `yaml:"displayName"`
	// Context is COLLECTION for actions with no specific resource instance
	// yet (create, list) or INSTANCE for actions on an existing one.
	Context string `yaml:"context"`
}

// ResourceEntry is one manifest row: a FHIR resource type and the catalog
// metadata the generator needs to emit its artifacts.
type ResourceEntry struct {
	FHIRType string `yaml:"fhirType"`
	Domain   string `yaml:"domain"`
	// Included defaults to true. Set it to false to record a resource that is
	// deliberately excluded from generation, along with Reason.
	Included *bool  `yaml:"included"`
	Reason   string `yaml:"reason"`

	// ResourceKey and DisplayName are derived from FHIRType, not read from
	// the manifest, so there is exactly one place a resource can disagree
	// with itself.
	ResourceKey string `yaml:"-"`
	Display     string `yaml:"-"`
}

// IsIncluded reports whether this entry should be generated. The zero value
// (nil) means "not stated in the manifest", which defaults to included.
func (r ResourceEntry) IsIncluded() bool {
	return r.Included == nil || *r.Included
}

// Manifest is the parsed, derived form of manifest.yaml.
type Manifest struct {
	SourceNote      string          `yaml:"sourceNote"`
	CatalogRevision int64           `yaml:"catalogRevision"`
	Actions         []Action        `yaml:"actions"`
	LockableActions []string        `yaml:"lockableActions"`
	Resources       []ResourceEntry `yaml:"resources"`
}

// LoadEmbeddedManifest parses the manifest.yaml committed alongside this
// package: the manifest that generates the deployed catalog.
func LoadEmbeddedManifest() (*Manifest, error) {
	raw, err := embeddedManifest.ReadFile("manifest.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading embedded manifest: %w", err)
	}
	return ParseManifest(raw)
}

// ParseManifest parses and validates manifest YAML from an arbitrary source,
// deriving ResourceKey and DisplayName for every entry. Used directly by
// tests exercising a small fixture manifest instead of the full catalog.
func ParseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	for i := range m.Resources {
		entry := &m.Resources[i]
		if entry.FHIRType == "" {
			return nil, fmt.Errorf("resource at index %d has no fhirType", i)
		}
		entry.ResourceKey = PascalToSnake(entry.FHIRType)
		entry.Display = DisplayName(entry.FHIRType)
		if !entry.IsIncluded() && entry.Reason == "" {
			return nil, fmt.Errorf("resource %s is excluded but records no reason", entry.FHIRType)
		}
	}

	if err := m.validate(); err != nil {
		return nil, err
	}

	return &m, nil
}

func (m *Manifest) validate() error {
	if len(m.Actions) == 0 {
		return fmt.Errorf("manifest declares no actions")
	}
	actionKeys := make(map[string]struct{}, len(m.Actions))
	for _, a := range m.Actions {
		if a.Key == "" {
			return fmt.Errorf("an action has no key")
		}
		if _, dup := actionKeys[a.Key]; dup {
			return fmt.Errorf("action %q is declared more than once", a.Key)
		}
		actionKeys[a.Key] = struct{}{}
	}
	for _, lockable := range m.LockableActions {
		if _, ok := actionKeys[lockable]; !ok {
			return fmt.Errorf("lockableActions references unknown action %q", lockable)
		}
	}

	seenKeys := make(map[string]string, len(m.Resources))
	seenTypes := make(map[string]struct{}, len(m.Resources))
	for _, entry := range m.Resources {
		if _, dup := seenTypes[entry.FHIRType]; dup {
			return fmt.Errorf("fhirType %q is declared more than once", entry.FHIRType)
		}
		seenTypes[entry.FHIRType] = struct{}{}

		if owner, dup := seenKeys[entry.ResourceKey]; dup {
			return fmt.Errorf("resource key %q is shared by %q and %q",
				entry.ResourceKey, owner, entry.FHIRType)
		}
		seenKeys[entry.ResourceKey] = entry.FHIRType
	}

	return nil
}

// IncludedResources returns the manifest resources with Included() true, in
// manifest order.
func (m *Manifest) IncludedResources() []ResourceEntry {
	var out []ResourceEntry
	for _, r := range m.Resources {
		if r.IsIncluded() {
			out = append(out, r)
		}
	}
	return out
}
