package cataloggen

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// resourceSchemaDoc mirrors the hand-authored patient_record.json shape
// (deploy/cerbos/policies/_schemas/patient_record.json): permissionContext is
// data only, never a verdict (§6.3, §21), and additionalProperties is false
// throughout so a caller cannot smuggle an extra attribute a future policy
// condition might read.
type resourceSchemaDoc struct {
	Schema               string                 `json:"$schema"`
	Title                string                 `json:"title"`
	Description          string                 `json:"description"`
	Type                 string                 `json:"type"`
	Required             []string               `json:"required"`
	AdditionalProperties bool                   `json:"additionalProperties"`
	Properties           map[string]interface{} `json:"properties"`
}

// RenderSchema renders the JSON schema for one manifest entry's resource
// attributes, generalising patient_record.json's shape to every generated
// resource.
func RenderSchema(entry ResourceEntry) (string, error) {
	doc := resourceSchemaDoc{
		Schema: "https://json-schema.org/draft/2020-12/schema",
		Title:  fmt.Sprintf("%s authorization attributes", entry.Display),
		Description: "Server-loaded resource attributes plus the permissionContext the ADS " +
			"assembles. permissionContext is data only: it carries the action sets and the " +
			"revision they were resolved at, never a verdict (§6.3, §21).",
		Type:                 "object",
		Required:             []string{"tenantId", "hospitalId", "status", "permissionContext"},
		AdditionalProperties: false,
		Properties: map[string]interface{}{
			"tenantId": map[string]interface{}{
				"type":      "string",
				"minLength": 1,
			},
			"hospitalId": map[string]interface{}{
				"type":      "string",
				"minLength": 1,
			},
			"status": map[string]interface{}{
				"type": "string",
				"enum": []string{"ACTIVE", "LOCKED", "ARCHIVED"},
			},
			"permissionContext": map[string]interface{}{
				"type": "object",
				"required": []string{
					"roleGrantedActions",
					"userGrantedActions",
					"userRevokedActions",
					"permissionRevision",
				},
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"roleGrantedActions": stringArraySchema(),
					"userGrantedActions": stringArraySchema(),
					"userRevokedActions": stringArraySchema(),
					"permissionRevision": map[string]interface{}{
						"type":    "integer",
						"minimum": 0,
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return "", fmt.Errorf("encoding schema for %s: %w", entry.FHIRType, err)
	}
	return buf.String(), nil
}

func stringArraySchema() map[string]interface{} {
	return map[string]interface{}{
		"type":  "array",
		"items": map[string]interface{}{"type": "string"},
	}
}
