package assignmentstore

import (
	"fmt"
	"regexp"
	"strings"
)

// NormalizeOverrideKey puts an override key into the one form the database
// stores.
//
// An absent instance id can arrive as "" from a caller or as the sentinel from a
// previous read, and Oracle turns "" into NULL on the way in. Normalising here,
// in the port rather than in each adapter, is what makes both spellings address
// the same row on both engines.
func NormalizeOverrideKey(key UserOverrideKey) UserOverrideKey {
	if key.ResourceInstanceID == "" {
		key.ResourceInstanceID = NoResourceInstance
	}
	return key
}

// ResourceActionKey formats one resource:action pair the way the audit
// search dimensions "resource" and "action" (§9.1) are stored and matched.
func ResourceActionKey(resourceKey, actionKey string) string {
	return resourceKey + ":" + actionKey
}

// JoinResourceActionKeys formats the resource:action pairs a single audit
// event touched, comma-separated, for AuditEvent.ResourceActionKeys.
func JoinResourceActionKeys(inputs []RolePermissionInput) string {
	keys := make([]string, 0, len(inputs))
	for _, input := range inputs {
		keys = append(keys, ResourceActionKey(input.ResourceKey, input.ActionKey))
	}
	return strings.Join(keys, ",")
}

// tableName is deliberately strict: table names reach SQL by concatenation in
// the adapters' Truncate, which is the one place a parameter cannot be bound.
var tableName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,29}$`)

// ValidateTableName rejects anything that is not a plain lower-case identifier
// within Oracle's 30-character comfort zone.
func ValidateTableName(name string) error {
	if !tableName.MatchString(name) {
		return fmt.Errorf("refusing to use %q as a table name", name)
	}
	return nil
}
