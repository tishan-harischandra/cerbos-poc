// Package permissioncontext assembles the authorization data the ADS sends to
// Cerbos for one principal and one resource.
//
// It reports facts and nothing else: which actions the principal's roles grant,
// which the user override grants, which it revokes, and the revision they were
// resolved at. It deliberately does not reconcile them. A revoke that cancels a
// role grant is precedence, precedence lives in Cerbos policy (§6.3, ADR-003),
// and a second implementation here would be the duplicated-logic failure mode
// §21 warns about.
package permissioncontext

import "sort"

// OverrideState is the state of a user override for a single action.
type OverrideState int

const (
	// Inherit defers to whatever the principal's roles grant.
	Inherit OverrideState = iota
	// Grant adds the action for this user.
	Grant
	// Revoke withdraws the action for this user.
	Revoke
)

// RolePermission is one action a canonical role either grants or does not.
type RolePermission struct {
	Role    string
	Action  string
	Enabled bool
}

// UserOverride is one user-level decision about a single action.
type UserOverride struct {
	Action string
	State  OverrideState
}

// Input is everything resolved for one principal and one resource.
type Input struct {
	RolePermissions []RolePermission
	UserOverrides   []UserOverride
	Revision        int64
}

// Context is the wire form Cerbos validates against the resource schema and
// reads in policy conditions as request.resource.attr.permissionContext.
type Context struct {
	RoleGrantedActions []string `json:"roleGrantedActions"`
	UserGrantedActions []string `json:"userGrantedActions"`
	UserRevokedActions []string `json:"userRevokedActions"`
	PermissionRevision int64    `json:"permissionRevision"`
}

// Assemble collects the action sets for one principal and resource.
//
// Each set is deduplicated and sorted so that an unchanged permission state
// always produces an identical request, which is what makes the assembled
// context safe to cache and compare.
func Assemble(in Input) Context {
	roleGranted := make(map[string]struct{})
	for _, permission := range in.RolePermissions {
		if permission.Enabled {
			roleGranted[permission.Action] = struct{}{}
		}
	}

	userGranted := make(map[string]struct{})
	userRevoked := make(map[string]struct{})
	for _, override := range in.UserOverrides {
		switch override.State {
		case Grant:
			userGranted[override.Action] = struct{}{}
		case Revoke:
			userRevoked[override.Action] = struct{}{}
		case Inherit:
		}
	}

	return Context{
		RoleGrantedActions: sortedActions(roleGranted),
		UserGrantedActions: sortedActions(userGranted),
		UserRevokedActions: sortedActions(userRevoked),
		PermissionRevision: in.Revision,
	}
}

// AsMap renders the context as the value Cerbos will carry in
// request.resource.attr.permissionContext.
//
// Cerbos transports attributes as protobuf Struct values, which accept only
// JSON-shaped Go values. A Go struct handed straight to the SDK is dropped
// without an error, and a resource whose permissionContext never arrived is
// denied for every action by schema enforcement. Converting here keeps that
// knowledge in the package that owns the wire contract.
func (c Context) AsMap() map[string]any {
	return map[string]any{
		"roleGrantedActions": asAnySlice(c.RoleGrantedActions),
		"userGrantedActions": asAnySlice(c.UserGrantedActions),
		"userRevokedActions": asAnySlice(c.UserRevokedActions),
		"permissionRevision": c.PermissionRevision,
	}
}

func asAnySlice(actions []string) []any {
	values := make([]any, 0, len(actions))
	for _, action := range actions {
		values = append(values, action)
	}
	return values
}

func sortedActions(set map[string]struct{}) []string {
	actions := make([]string, 0, len(set))
	for action := range set {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}
