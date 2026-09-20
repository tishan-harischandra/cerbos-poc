// Package cataloggen turns a FHIR resource manifest into every downstream
// authorization artifact: the resource-action catalog, one Cerbos resource
// policy per resource, the principal and per-resource JSON schemas, the
// exhaustive Cerbos test suite and the database catalog seed (issue #8,
// §6.1, §6.5, §8, §19.1, §21).
//
// The manifest is the only hand-edited input, and it is the adopter's to
// author, not the platform's to contain: this package reads it from a path
// its caller chooses (ADR-013). manifest.yaml alongside this file is the
// example domain model this repository generates its committed catalog from
// and the fixture its tests use - see DefaultManifestPath. Everything this
// package emits is generated, golden-file tested and reviewed as a diff when
// the manifest or the generator changes.
package cataloggen

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

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

// DefaultManifestPath is where this repository keeps the example domain model
// the committed catalog is generated from, relative to the repository root.
// It is the generators' default, not a fallback: an installation sourcing its
// manifest from an adopter's repository (ADR-013) passes a path instead, and
// nothing reads this one unless it was asked to.
const DefaultManifestPath = "libs/cataloggen/manifest.yaml"

// LoadManifestFile reads and parses the manifest at path. Errors name the
// path, because the caller chose it and a manifest that is absent or malformed
// is a configuration mistake to be corrected there; there is deliberately no
// fallback to any other manifest.
func LoadManifestFile(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("manifest %s: %w", path, err)
	}
	return manifest, nil
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
