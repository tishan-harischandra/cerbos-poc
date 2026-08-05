// Package canonicalid builds the §7.5 canonical role identifiers.
//
// It exists so there is exactly one place that knows the spelling. Token
// normalisation and the identity directory adapter both produce identifiers
// that are compared against the role-permission matrix, and §7.5 requires them
// to be identical. Two implementations of the same format would agree until the
// day one of them changed, and the symptom would be a principal that silently
// resolves to no permissions rather than an error anybody could see.
package canonicalid

import "strings"

// RealmRoleSegment is the client segment used for a Keycloak realm role, which
// belongs to the realm rather than to any client.
const RealmRoleSegment = "realm"

// ReservedPrefix marks the synthetic roles only the platform may assign. §16.1
// requires a token carrying one to be rejected rather than merely ignored.
const ReservedPrefix = "sys:"

// KeycloakRealmRole renders `kc:<realm>:realm:<role-id>`.
func KeycloakRealmRole(realm, roleID string) string {
	return KeycloakClientRole(realm, RealmRoleSegment, roleID)
}

// KeycloakClientRole renders `kc:<realm>:<client-id>:<role-id>`.
//
// For this prototype the role-id segment carries the Keycloak role *name*.
// Access tokens carry role names and nothing else, so a UUID-based identifier
// could never be reconstructed from a token without a directory round trip on
// the decision hot path. §7.3's preference for stable IDs is therefore honoured
// by the directory adapter's display metadata, not by this identifier.
func KeycloakClientRole(realm, clientID, roleID string) string {
	return strings.Join([]string{"kc", realm, clientID, roleID}, ":")
}

// WSO2Role renders `wso2:<tenant-domain>:<role-id>`.
func WSO2Role(tenantDomain, roleID string) string {
	return strings.Join([]string{"wso2", tenantDomain, roleID}, ":")
}

// IsReserved reports whether a role is one the platform reserves for itself.
// The comparison is case-insensitive because the check is a security control:
// a claim of `SYS:permission-evaluator` is the same attempt as `sys:`.
func IsReserved(role string) bool {
	return strings.HasPrefix(strings.ToLower(role), ReservedPrefix)
}
