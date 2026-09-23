package keycloakbulkload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OrganizationGroupIDs reads the internal group id backing each of realmID's
// organizations, keyed by alias (issue #87).
//
// A Keycloak organization is a KEYCLOAK_GROUP row (type = 1) that an ORG row
// points at by group_id; membership is an ordinary USER_GROUP_MEMBERSHIP row
// against that group id, not against the organization's own id. Measured
// directly against a real Keycloak 26.7.3 - see docs/MEASURED_FINDINGS.md,
// "Keycloak 26.7.3's organization schema" - because Keycloak documents neither
// table. The Admin REST organization representation does not expose the
// group id at all, so a membership-scale bulk write has no path to it other
// than this direct read.
func OrganizationGroupIDs(ctx context.Context, pool *pgxpool.Pool, realmID string) (map[string]string, error) {
	rows, err := pool.Query(ctx, `SELECT alias, group_id FROM org WHERE realm_id = $1`, realmID)
	if err != nil {
		return nil, fmt.Errorf("keycloakbulkload: reading organization group ids: %w", err)
	}
	defer rows.Close()

	groupIDByAlias := make(map[string]string)
	for rows.Next() {
		var alias, groupID string
		if err := rows.Scan(&alias, &groupID); err != nil {
			return nil, fmt.Errorf("keycloakbulkload: scanning an organization row: %w", err)
		}
		groupIDByAlias[alias] = groupID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("keycloakbulkload: reading organization group ids: %w", err)
	}
	return groupIDByAlias, nil
}

// OrganizationRoleGroupIDs reads each native organization role subgroup,
// keyed first by organization alias and then by the mapped realm-role name.
// Every join is anchored to the requested realm and to ORG's exact backing
// group; neither a reusable group name nor a role name can imply ownership.
func OrganizationRoleGroupIDs(ctx context.Context, pool *pgxpool.Pool, realmID string) (map[string]map[string]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT o.alias, r.name, role_group.id
		FROM org o
		JOIN keycloak_group organization_group
		  ON organization_group.id = o.group_id
		 AND organization_group.realm_id = o.realm_id
		 AND organization_group.org_id = o.id
		 AND organization_group.type = 1
		JOIN keycloak_group role_group
		  ON role_group.parent_group = organization_group.id
		 AND role_group.realm_id = o.realm_id
		 AND role_group.org_id = o.id
		 AND role_group.type = 1
		JOIN group_role_mapping mapping ON mapping.group_id = role_group.id
		JOIN keycloak_role r
		  ON r.id = mapping.role_id
		 AND r.realm_id = o.realm_id
		 AND r.client_role = false
		 AND r.client_realm_constraint = o.realm_id
		WHERE o.realm_id = $1`, realmID)
	if err != nil {
		return nil, fmt.Errorf("keycloakbulkload: reading organization role group ids: %w", err)
	}
	defer rows.Close()

	groupIDs := make(map[string]map[string]string)
	for rows.Next() {
		var alias, roleName, groupID string
		if err := rows.Scan(&alias, &roleName, &groupID); err != nil {
			return nil, fmt.Errorf("keycloakbulkload: scanning an organization role group: %w", err)
		}
		if groupIDs[alias] == nil {
			groupIDs[alias] = make(map[string]string)
		}
		if existing, ok := groupIDs[alias][roleName]; ok && existing != groupID {
			return nil, fmt.Errorf("keycloakbulkload: organization %q has multiple groups mapped to realm role %q", alias, roleName)
		}
		groupIDs[alias][roleName] = groupID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("keycloakbulkload: reading organization role group ids: %w", err)
	}
	return groupIDs, nil
}

// GroupType reads Keycloak's Admin REST representation of a group. It is used
// by the schema integration test to verify SQL-discovered role groups remain
// native GroupModel.Type.ORGANIZATION groups at the public model boundary.
func (c *AdminClient) GroupType(ctx context.Context, realm, alias, groupID string) (string, error) {
	status, body, err := c.call(ctx, http.MethodGet, "/admin/realms/"+realm+"/organizations?first=0&max=10000", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("keycloakbulkload: listing organizations in realm %q failed with %d: %s", realm, status, body)
	}
	var organizations []struct {
		ID    string `json:"id"`
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal(body, &organizations); err != nil {
		return "", fmt.Errorf("keycloakbulkload: decoding organizations in realm %q: %w", realm, err)
	}
	var organizationID string
	for _, organization := range organizations {
		if organization.Alias == alias {
			organizationID = organization.ID
			break
		}
	}
	if organizationID == "" {
		return "", fmt.Errorf("keycloakbulkload: organization %q was not found in realm %q", alias, realm)
	}
	status, body, err = c.call(ctx, http.MethodGet,
		"/admin/realms/"+realm+"/organizations/"+organizationID+"/groups", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("keycloakbulkload: reading groups for organization %q failed with %d: %s", alias, status, body)
	}
	var groups []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &groups); err != nil {
		return "", fmt.Errorf("keycloakbulkload: decoding groups for organization %q: %w", alias, err)
	}
	for _, group := range groups {
		if group.ID != groupID {
			continue
		}
		if group.Type != "" {
			return group.Type, nil
		}
		// Keycloak 26.7.3's organization endpoint omits the type field from
		// GroupRepresentation. Reading the same id through the ordinary group
		// endpoint is the public-model discriminator: it rejects type
		// ORGANIZATION with this explicit error rather than returning a normal
		// group representation.
		status, body, err = c.call(ctx, http.MethodGet, "/admin/realms/"+realm+"/groups/"+groupID, nil)
		if err != nil {
			return "", err
		}
		if status == http.StatusBadRequest && bytes.Contains(body, []byte("organization related group")) {
			return "ORGANIZATION", nil
		}
		return "", fmt.Errorf("keycloakbulkload: group %q did not read back as GroupModel.Type.ORGANIZATION (status %d: %s)", groupID, status, body)
	}
	return "", fmt.Errorf("keycloakbulkload: group %q was not found in organization %q", groupID, alias)
}

// EnsureOrganizationRoleGroups creates one native organization subgroup per
// role in every organization and maps the corresponding realm role to it.
// This low-volume catalog setup deliberately uses Admin REST; only per-user
// memberships need the direct-SQL bulk path.
func (c *AdminClient) EnsureOrganizationRoleGroups(ctx context.Context, realm string, aliases, roleNames []string) error {
	roleByName := make(map[string]map[string]any, len(roleNames))
	for _, roleName := range roleNames {
		status, body, err := c.call(ctx, http.MethodPost, "/admin/realms/"+realm+"/roles", map[string]any{"name": roleName})
		if err != nil {
			return err
		}
		if status != http.StatusCreated && status != http.StatusConflict {
			return fmt.Errorf("keycloakbulkload: creating realm role %q failed with %d: %s", roleName, status, body)
		}
		status, body, err = c.call(ctx, http.MethodGet, "/admin/realms/"+realm+"/roles/"+url.PathEscape(roleName), nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("keycloakbulkload: looking up realm role %q failed with %d: %s", roleName, status, body)
		}
		var role map[string]any
		if err := json.Unmarshal(body, &role); err != nil {
			return fmt.Errorf("keycloakbulkload: decoding realm role %q: %w", roleName, err)
		}
		roleByName[roleName] = role
	}

	status, body, err := c.call(ctx, http.MethodGet, "/admin/realms/"+realm+"/organizations?first=0&max=10000", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("keycloakbulkload: listing organizations in realm %q failed with %d: %s", realm, status, body)
	}
	var organizations []struct {
		ID    string `json:"id"`
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal(body, &organizations); err != nil {
		return fmt.Errorf("keycloakbulkload: decoding organizations in realm %q: %w", realm, err)
	}
	organizationIDByAlias := make(map[string]string, len(organizations))
	for _, organization := range organizations {
		organizationIDByAlias[organization.Alias] = organization.ID
	}

	for _, alias := range aliases {
		organizationID, ok := organizationIDByAlias[alias]
		if !ok {
			return fmt.Errorf("keycloakbulkload: organization %q was not found in realm %q", alias, realm)
		}
		groupsPath := "/admin/realms/" + realm + "/organizations/" + organizationID + "/groups"
		for _, roleName := range roleNames {
			status, body, err := c.call(ctx, http.MethodPost, groupsPath, map[string]any{"name": roleName})
			if err != nil {
				return err
			}
			if status != http.StatusCreated && status != http.StatusConflict {
				return fmt.Errorf("keycloakbulkload: creating organization role group %q/%q failed with %d: %s", alias, roleName, status, body)
			}
		}

		status, body, err := c.call(ctx, http.MethodGet, groupsPath, nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("keycloakbulkload: listing organization role groups for %q failed with %d: %s", alias, status, body)
		}
		var groups []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &groups); err != nil {
			return fmt.Errorf("keycloakbulkload: decoding organization role groups for %q: %w", alias, err)
		}
		groupIDByName := make(map[string]string, len(groups))
		for _, group := range groups {
			groupIDByName[group.Name] = group.ID
		}
		for _, roleName := range roleNames {
			groupID, ok := groupIDByName[roleName]
			if !ok {
				return fmt.Errorf("keycloakbulkload: organization role group %q/%q was not found after creation", alias, roleName)
			}
			mappingPath := groupsPath + "/" + groupID + "/role-mappings/realm"
			status, body, err := c.call(ctx, http.MethodPost, mappingPath, []map[string]any{roleByName[roleName]})
			if err != nil {
				return err
			}
			if status != http.StatusNoContent {
				return fmt.Errorf("keycloakbulkload: mapping realm role %q to organization %q failed with %d: %s", roleName, alias, status, body)
			}
		}
	}
	return nil
}
