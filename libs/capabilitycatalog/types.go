// Package capabilitycatalog implements the composite UI-capability catalog
// contract (§12.1, §12.2, issue #10): loading capability definitions,
// validating them against the active resource/action catalog, and
// instantiating the five archetypes across the FHIR resource catalog.
//
// A UI capability is a named boolean expression whose leaves are Cerbos
// permission decisions (§12.1). Composition never changes Cerbos permission
// semantics; this package only assembles and validates the expression tree,
// exactly like the ADS assembles permissionContext data rather than deciding
// anything (§6.3, §21).
package capabilitycatalog

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// PermissionRequirement is one leaf of a capability expression: a single
// Cerbos resource-action permission, resolved server-side against a
// targetRef (§12.2).
type PermissionRequirement struct {
	Resource  string `yaml:"resource" json:"resource"`
	Action    string `yaml:"action" json:"action"`
	TargetRef string `yaml:"targetRef" json:"targetRef"`
}

// Expression is the CapabilityExpression union from §12.2: exactly one of a
// permission leaf, an allOf, an anyOf, or a capabilityRef. capabilityRef is
// not part of the §12.2 contract text - capability-to-capability references
// are "discouraged initially" - but the type still needs to exist so
// Validate can reject a cycle if one is ever authored (issue #10 acceptance
// criterion: "Validation rejects a circular reference").
type Expression struct {
	Permission    *PermissionRequirement `json:"permission,omitempty"`
	AllOf         []Expression           `json:"allOf,omitempty"`
	AnyOf         []Expression           `json:"anyOf,omitempty"`
	CapabilityRef string                 `json:"capabilityRef,omitempty"`
}

// negationKeys are expression keys that would express a negation. §12.2
// says "Negation is not supported in the initial design"; these are
// rejected explicitly, with a message calling out what they are, rather
// than falling through to the generic "unknown key" error.
var negationKeys = map[string]bool{
	"not":    true,
	"negate": true,
	"except": true,
	"deny":   true,
	"none":   true,
}

// UnmarshalYAML strictly validates the expression's shape: exactly one
// recognised key, non-empty allOf/anyOf arrays, and a clear rejection of any
// negation-shaped key. It intentionally does not know which capability it
// belongs to; UiCapabilityDefinition.UnmarshalYAML wraps errors from here
// with the capability key so every validation error names its capability.
func (e *Expression) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expression must be a mapping, got %v", node.Kind)
	}

	var permissionNode, allOfNode, anyOfNode, capabilityRefNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		switch keyNode.Value {
		case "permission":
			permissionNode = valNode
		case "allOf":
			allOfNode = valNode
		case "anyOf":
			anyOfNode = valNode
		case "capabilityRef":
			capabilityRefNode = valNode
		default:
			if negationKeys[keyNode.Value] {
				return fmt.Errorf("expression key %q expresses negation, which §12.2 does not support", keyNode.Value)
			}
			return fmt.Errorf("unknown expression key %q", keyNode.Value)
		}
	}

	present := 0
	for _, n := range []*yaml.Node{permissionNode, allOfNode, anyOfNode, capabilityRefNode} {
		if n != nil {
			present++
		}
	}
	if present != 1 {
		return fmt.Errorf("expression must have exactly one of permission, allOf, anyOf, capabilityRef")
	}

	switch {
	case permissionNode != nil:
		var p PermissionRequirement
		if err := permissionNode.Decode(&p); err != nil {
			return fmt.Errorf("decoding permission: %w", err)
		}
		e.Permission = &p
	case allOfNode != nil:
		if allOfNode.Kind != yaml.SequenceNode || len(allOfNode.Content) == 0 {
			return fmt.Errorf("allOf must be a non-empty array")
		}
		var items []Expression
		if err := allOfNode.Decode(&items); err != nil {
			return fmt.Errorf("decoding allOf: %w", err)
		}
		e.AllOf = items
	case anyOfNode != nil:
		if anyOfNode.Kind != yaml.SequenceNode || len(anyOfNode.Content) == 0 {
			return fmt.Errorf("anyOf must be a non-empty array")
		}
		var items []Expression
		if err := anyOfNode.Decode(&items); err != nil {
			return fmt.Errorf("decoding anyOf: %w", err)
		}
		e.AnyOf = items
	case capabilityRefNode != nil:
		e.CapabilityRef = capabilityRefNode.Value
	}

	return nil
}

// UiCapabilityDefinition is the UiCapabilityDefinition contract from §12.2.
type UiCapabilityDefinition struct {
	Key             string     `yaml:"key" json:"key"`
	Module          string     `yaml:"module" json:"module"`
	Context         string     `yaml:"context" json:"context"`
	Expression      Expression `yaml:"expression" json:"expression"`
	CatalogRevision int64      `yaml:"-" json:"catalogRevision"`
}

// UnmarshalYAML decodes the key/module/context fields directly, then decodes
// the expression separately so a failure inside it can be wrapped with the
// capability key: every loader-detected error names its capability, as the
// issue #10 acceptance criteria require.
func (d *UiCapabilityDefinition) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Key        string    `yaml:"key"`
		Module     string    `yaml:"module"`
		Context    string    `yaml:"context"`
		Expression yaml.Node `yaml:"expression"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	if raw.Key == "" {
		return fmt.Errorf("capability definition has no key")
	}

	d.Key = raw.Key
	d.Module = raw.Module
	d.Context = raw.Context

	var expr Expression
	if err := raw.Expression.Decode(&expr); err != nil {
		return fmt.Errorf("capability %q: %w", raw.Key, err)
	}
	d.Expression = expr

	return nil
}
