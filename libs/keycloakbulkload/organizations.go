package keycloakbulkload

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OrganizationGroupIDs reads the internal group id backing each of realmID's
// organizations, keyed by alias (issue #87).
//
// A Keycloak organization is a KEYCLOAK_GROUP row (type = 1) that an ORG row
// points at by group_id; membership is an ordinary USER_GROUP_MEMBERSHIP row
// against that group id, not against the organization's own id. Measured
// directly against a real Keycloak 26.4 - see docs/MEASURED_FINDINGS.md,
// "Keycloak 26.4's organization schema" - because Keycloak documents neither
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
