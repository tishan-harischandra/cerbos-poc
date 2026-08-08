package loadmodel

import (
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// RolePermissions returns one role_permission row per (tenant, canonical
// role, action) - every canonical role in every tenant grants every action
// on ResourceKey, so a decision against any generated user's roles always
// finds a matching row. at is the instant validity windows are laid out
// around (mirrors demoseed.Apply's own at parameter).
func (p *Population) RolePermissions(at time.Time) []assignmentstore.RolePermission {
	began := at.AddDate(-1, 0, 0)
	ends := at.AddDate(1, 0, 0)

	permissions := make([]assignmentstore.RolePermission, 0, len(p.tenantIDs)*len(p.roleNames)*len(Actions))
	for _, tenant := range p.tenantIDs {
		for _, role := range p.roleNames {
			for _, action := range Actions {
				permissions = append(permissions, assignmentstore.RolePermission{
					Key: assignmentstore.RolePermissionKey{
						TenantID:       tenant,
						RoleExternalID: role,
						ResourceKey:    ResourceKey,
						ActionKey:      action,
					},
					Enabled:    true,
					ValidFrom:  began,
					ValidUntil: ends,
					Revision:   1,
				})
			}
		}
	}
	return permissions
}

// overrideEffectAndValidity picks the override's effect and validity window
// from c, the override's position (0-based) within its user's overrides: the
// rotation guarantees the documented mix of GRANT, REVOKE and expired rows
// (issue #24's acceptance criterion) rather than leaving it to chance.
func overrideEffectAndValidity(c int, began, ends, expired time.Time) (assignmentstore.OverrideEffect, bool, time.Time, time.Time) {
	switch c % 3 {
	case 0:
		return assignmentstore.EffectGrant, true, began, ends
	case 1:
		return assignmentstore.EffectRevoke, true, began, ends
	default:
		return assignmentstore.EffectGrant, true, began, expired
	}
}

// Overrides returns the index-th user's overrides, or nil if
// HasOverrides(index) is false. Users with overrides carry 1 to 10 of them
// (1 + index%10), mixing GRANT, REVOKE and expired per
// overrideEffectAndValidity.
//
// A user override's key is (tenant, hospital, user, resource_key,
// action_key, resource_instance_id); with only len(Actions) real actions on
// ResourceKey, cycling the action alone would collide once c passes
// len(Actions) and silently upsert over an earlier row rather than adding a
// distinct one. The first len(Actions) overrides stay resource-wide
// (NoResourceInstance, one per action); any beyond that are scoped to a
// synthetic instance unique to c, keeping every one of the up-to-10 rows
// distinct as issue #24 requires.
func (p *Population) Overrides(index int, at time.Time) []assignmentstore.UserOverride {
	if !p.HasOverrides(index) {
		return nil
	}
	began := at.AddDate(-1, 0, 0)
	ends := at.AddDate(1, 0, 0)
	expired := at.AddDate(0, -1, 0)

	user := p.User(index)
	// rank is this override user's position among override users (0, 1, 2,
	// ...), not the raw index, which would correlate with
	// OverrideEveryNthUser and could make count trivially constant.
	rank := index / p.cfg.OverrideEveryNthUser
	count := 1 + rank%10
	overrides := make([]assignmentstore.UserOverride, 0, count)
	for c := 0; c < count; c++ {
		action := Actions[c%len(Actions)]
		instance := assignmentstore.NoResourceInstance
		if c >= len(Actions) {
			instance = fmt.Sprintf("override-instance-%03d", c)
		}
		effect, enabled, from, until := overrideEffectAndValidity(c, began, ends, expired)
		overrides = append(overrides, assignmentstore.UserOverride{
			Key: assignmentstore.UserOverrideKey{
				TenantID:           user.TenantID,
				HospitalID:         user.HospitalID,
				UserExternalID:     user.Username,
				ResourceKey:        ResourceKey,
				ActionKey:          action,
				ResourceInstanceID: instance,
			},
			Effect:     effect,
			Enabled:    enabled,
			ValidFrom:  from,
			ValidUntil: until,
			Revision:   1,
		})
	}
	return overrides
}

// Resources returns every FHIR resource instance the population's
// ResourceTypes call for: ActiveInstancesPerResourceType ACTIVE and
// LockedInstancesPerResourceType LOCKED instances, per tenant, per hospital,
// per resource type. LOCKED instances exist across every configured type, as
// issue #24's acceptance criteria ask for.
func (p *Population) Resources(at time.Time) []assignmentstore.Resource {
	var resources []assignmentstore.Resource
	for _, tenant := range p.tenantIDs {
		for _, hospital := range p.hospitals[tenant] {
			for _, resourceType := range p.cfg.ResourceTypes {
				for i := 0; i < p.cfg.ActiveInstancesPerResourceType; i++ {
					resources = append(resources, resourceInstance(resourceType,
						fmt.Sprintf("%s-%s-active-%03d", hospital, resourceType, i),
						tenant, hospital, "ACTIVE", at))
				}
				for i := 0; i < p.cfg.LockedInstancesPerResourceType; i++ {
					resources = append(resources, resourceInstance(resourceType,
						fmt.Sprintf("%s-%s-locked-%03d", hospital, resourceType, i),
						tenant, hospital, "LOCKED", at))
				}
			}
		}
	}
	return resources
}

func resourceInstance(resourceType, id, tenant, hospital, status string, at time.Time) assignmentstore.Resource {
	return assignmentstore.Resource{
		ResourceType: resourceType,
		ResourceID:   id,
		TenantID:     tenant,
		HospitalID:   hospital,
		Status:       status,
		PayloadJSON:  fmt.Sprintf(`{"resourceType":%q,"id":%q}`, resourceType, id),
		UpdatedAt:    at,
	}
}

// PermissionRevisions returns one PermissionRevision per tenant, all at the
// same revision: the load model's matrix is written once, not incrementally
// revised, so every tenant's revision advances together.
func (p *Population) PermissionRevisions(at time.Time) []assignmentstore.PermissionRevision {
	revisions := make([]assignmentstore.PermissionRevision, 0, len(p.tenantIDs))
	for _, tenant := range p.tenantIDs {
		revisions = append(revisions, assignmentstore.PermissionRevision{
			TenantID:  tenant,
			Revision:  1,
			ChangedAt: at,
		})
	}
	return revisions
}
