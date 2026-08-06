// Package postgresstore is the PostgreSQL adapter for the assignmentstore port.
//
// All PostgreSQL-specific SQL in the system lives here and in the Oracle
// adapter's equivalent. Callers see only the port, so a dialect difference is
// this package's problem to absorb rather than a caller's to branch on.
package postgresstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Store is the PostgreSQL implementation of assignmentstore.Store.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to PostgreSQL. The DSN is a libpq connection string or URL.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("postgresstore: no DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: connecting: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Ping reports whether PostgreSQL is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close releases the pool.
func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

// Schema reports the shape of the migrated schema.
func (s *Store) Schema() assignmentstore.SchemaInspector {
	return inspector{pool: s.pool}
}

// SaveRolePermission inserts or updates one role permission on its §8.2 key.
func (s *Store) SaveRolePermission(ctx context.Context, permission assignmentstore.RolePermission) error {
	const statement = `
		INSERT INTO role_permission (
			tenant_id, role_external_id, resource_key, action_key,
			enabled, valid_from, valid_until, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, role_external_id, resource_key, action_key)
		DO UPDATE SET
			enabled = EXCLUDED.enabled,
			valid_from = EXCLUDED.valid_from,
			valid_until = EXCLUDED.valid_until,
			revision = EXCLUDED.revision`

	key := permission.Key
	_, err := s.pool.Exec(ctx, statement,
		key.TenantID, key.RoleExternalID, key.ResourceKey, key.ActionKey,
		permission.Enabled, permission.ValidFrom, endOrNull(permission.ValidUntil), permission.Revision)
	if err != nil {
		return fmt.Errorf("postgresstore: saving a role permission: %w", err)
	}
	return nil
}

// RolePermission reads one role permission by its §8.2 key.
func (s *Store) RolePermission(ctx context.Context, key assignmentstore.RolePermissionKey) (assignmentstore.RolePermission, bool, error) {
	const query = `
		SELECT enabled, valid_from, valid_until, revision
		FROM role_permission
		WHERE tenant_id = $1 AND role_external_id = $2
		  AND resource_key = $3 AND action_key = $4`

	permission := assignmentstore.RolePermission{Key: key}
	var validUntil *time.Time
	err := s.pool.QueryRow(ctx, query,
		key.TenantID, key.RoleExternalID, key.ResourceKey, key.ActionKey).
		Scan(&permission.Enabled, &permission.ValidFrom, &validUntil, &permission.Revision)
	if isNoRows(err) {
		return assignmentstore.RolePermission{}, false, nil
	}
	if err != nil {
		return assignmentstore.RolePermission{}, false, fmt.Errorf("postgresstore: reading a role permission: %w", err)
	}
	if validUntil != nil {
		permission.ValidUntil = *validUntil
	}
	return permission, true, nil
}

// ActiveRolePermissions reads the whole role matrix for a set of roles in one
// round trip.
//
// The role set is bound as a single array parameter rather than expanded into a
// placeholder per role. A generated IN list would give the planner a differently
// shaped statement for every role count, defeating the prepared-statement cache
// on the hottest query in the system.
func (s *Store) ActiveRolePermissions(ctx context.Context, query assignmentstore.ActiveRolePermissionQuery) ([]assignmentstore.RolePermission, error) {
	if len(query.RoleExternalIDs) == 0 {
		return nil, nil
	}

	const statement = `
		SELECT role_external_id, action_key, enabled, valid_from, valid_until, revision
		FROM role_permission
		WHERE tenant_id = $1
		  AND role_external_id = ANY($2)
		  AND resource_key = $3
		  AND valid_from <= $4
		  AND (valid_until IS NULL OR valid_until > $4)`

	rows, err := s.pool.Query(ctx, statement,
		query.TenantID, query.RoleExternalIDs, query.ResourceKey, query.At)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: reading the active role matrix: %w", err)
	}
	defer rows.Close()

	var permissions []assignmentstore.RolePermission
	for rows.Next() {
		permission := assignmentstore.RolePermission{
			Key: assignmentstore.RolePermissionKey{
				TenantID:    query.TenantID,
				ResourceKey: query.ResourceKey,
			},
		}
		var validUntil *time.Time
		if err := rows.Scan(&permission.Key.RoleExternalID, &permission.Key.ActionKey,
			&permission.Enabled, &permission.ValidFrom, &validUntil, &permission.Revision); err != nil {
			return nil, fmt.Errorf("postgresstore: scanning a role permission: %w", err)
		}
		if validUntil != nil {
			permission.ValidUntil = *validUntil
		}
		permissions = append(permissions, permission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: reading the active role matrix: %w", err)
	}
	return permissions, nil
}

// RolePermissionsForRole reads every permission row a role carries across
// every resource, unfiltered by validity or enabled state (§9.2's role
// matrix screen).
func (s *Store) RolePermissionsForRole(ctx context.Context, tenantID, roleExternalID string) ([]assignmentstore.RolePermission, error) {
	const query = `
		SELECT resource_key, action_key, enabled, valid_from, valid_until, revision
		FROM role_permission
		WHERE tenant_id = $1 AND role_external_id = $2`

	rows, err := s.pool.Query(ctx, query, tenantID, roleExternalID)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: reading a role's permissions: %w", err)
	}
	defer rows.Close()

	var permissions []assignmentstore.RolePermission
	for rows.Next() {
		permission := assignmentstore.RolePermission{
			Key: assignmentstore.RolePermissionKey{TenantID: tenantID, RoleExternalID: roleExternalID},
		}
		var validUntil *time.Time
		if err := rows.Scan(&permission.Key.ResourceKey, &permission.Key.ActionKey,
			&permission.Enabled, &permission.ValidFrom, &validUntil, &permission.Revision); err != nil {
			return nil, fmt.Errorf("postgresstore: scanning a role permission: %w", err)
		}
		if validUntil != nil {
			permission.ValidUntil = *validUntil
		}
		permissions = append(permissions, permission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: reading a role's permissions: %w", err)
	}
	return permissions, nil
}

// endOrNull maps the zero instant to a database NULL. A grant with no planned
// end is the ordinary case, and storing year zero instead would make it look
// like a grant that expired before it began.
func endOrNull(end time.Time) *time.Time {
	if end.IsZero() {
		return nil
	}
	return &end
}

// SaveUserOverride inserts or updates one override on its §8.2 key.
func (s *Store) SaveUserOverride(ctx context.Context, override assignmentstore.UserOverride) error {
	const statement = `
		INSERT INTO user_permission_override (
			tenant_id, hospital_id, user_external_id, resource_key, action_key,
			resource_instance_id, effect, enabled, valid_from, valid_until, revision, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (tenant_id, hospital_id, user_external_id, resource_key,
			action_key, resource_instance_id)
		DO UPDATE SET
			effect = EXCLUDED.effect,
			enabled = EXCLUDED.enabled,
			valid_from = EXCLUDED.valid_from,
			valid_until = EXCLUDED.valid_until,
			revision = EXCLUDED.revision,
			reason = EXCLUDED.reason`

	key := assignmentstore.NormalizeOverrideKey(override.Key)
	_, err := s.pool.Exec(ctx, statement,
		key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
		key.ActionKey, key.ResourceInstanceID, string(override.Effect),
		override.Enabled, override.ValidFrom, override.ValidUntil, override.Revision,
		nullableString(override.Reason))
	if err != nil {
		return fmt.Errorf("postgresstore: saving a user override: %w", err)
	}
	return nil
}

// UserOverride reads one override by its §8.2 key.
func (s *Store) UserOverride(ctx context.Context, key assignmentstore.UserOverrideKey) (assignmentstore.UserOverride, bool, error) {
	const query = `
		SELECT effect, enabled, valid_from, valid_until, revision, reason
		FROM user_permission_override
		WHERE tenant_id = $1 AND hospital_id = $2 AND user_external_id = $3
		  AND resource_key = $4 AND action_key = $5 AND resource_instance_id = $6`

	key = assignmentstore.NormalizeOverrideKey(key)
	override := assignmentstore.UserOverride{Key: key}
	var effect string
	var validUntil *time.Time
	var reason *string
	err := s.pool.QueryRow(ctx, query,
		key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
		key.ActionKey, key.ResourceInstanceID).
		Scan(&effect, &override.Enabled, &override.ValidFrom, &validUntil, &override.Revision, &reason)
	if isNoRows(err) {
		return assignmentstore.UserOverride{}, false, nil
	}
	if err != nil {
		return assignmentstore.UserOverride{}, false, fmt.Errorf("postgresstore: reading a user override: %w", err)
	}
	override.Effect = assignmentstore.OverrideEffect(effect)
	if validUntil != nil {
		override.ValidUntil = *validUntil
	}
	if reason != nil {
		override.Reason = *reason
	}
	return override, true, nil
}

// nullableString turns an empty string into a genuine SQL NULL rather than
// storing "" - reason is absent for INHERIT and for rows SaveUserOverride
// seeds directly, and a NULL is what a later SELECT tells apart from an
// administrator who wrote an empty-but-present reason, if that ever became
// something worth distinguishing.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// ActiveUserOverrides reads every override in force for one principal and one
// resource in one round trip, the same shape of read as ActiveRolePermissions.
//
// A query naming a resource instance reads both the tenant/hospital-wide row
// (the sentinel) and any row scoped to that exact instance; a query naming no
// instance reads only the wide row. Passing the same value twice when no
// instance is named keeps the statement identical either way, which is what
// lets Postgres reuse one prepared plan for both shapes of call.
func (s *Store) ActiveUserOverrides(ctx context.Context, query assignmentstore.ActiveUserOverridesQuery) ([]assignmentstore.UserOverride, error) {
	instance := query.ResourceInstanceID
	if instance == "" {
		instance = assignmentstore.NoResourceInstance
	}

	const statement = `
		SELECT action_key, resource_instance_id, effect, enabled, valid_from, valid_until, revision, reason
		FROM user_permission_override
		WHERE tenant_id = $1
		  AND hospital_id = $2
		  AND user_external_id = $3
		  AND resource_key = $4
		  AND resource_instance_id = ANY($5)
		  AND valid_from <= $6
		  AND (valid_until IS NULL OR valid_until > $6)`

	rows, err := s.pool.Query(ctx, statement,
		query.TenantID, query.HospitalID, query.UserExternalID, query.ResourceKey,
		[]string{assignmentstore.NoResourceInstance, instance}, query.At)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: reading the active user overrides: %w", err)
	}
	defer rows.Close()

	var overrides []assignmentstore.UserOverride
	for rows.Next() {
		override := assignmentstore.UserOverride{
			Key: assignmentstore.UserOverrideKey{
				TenantID: query.TenantID, HospitalID: query.HospitalID,
				UserExternalID: query.UserExternalID, ResourceKey: query.ResourceKey,
			},
		}
		var effect string
		var validUntil *time.Time
		var reason *string
		if err := rows.Scan(&override.Key.ActionKey, &override.Key.ResourceInstanceID,
			&effect, &override.Enabled, &override.ValidFrom, &validUntil, &override.Revision, &reason); err != nil {
			return nil, fmt.Errorf("postgresstore: scanning a user override: %w", err)
		}
		override.Effect = assignmentstore.OverrideEffect(effect)
		if validUntil != nil {
			override.ValidUntil = *validUntil
		}
		if reason != nil {
			override.Reason = *reason
		}
		overrides = append(overrides, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: reading the active user overrides: %w", err)
	}
	return overrides, nil
}

// SavePermissionRevision advances a tenant's revision in place.
func (s *Store) SavePermissionRevision(ctx context.Context, revision assignmentstore.PermissionRevision) error {
	const statement = `
		INSERT INTO permission_revision (tenant_id, revision, changed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET
			revision = EXCLUDED.revision,
			changed_at = EXCLUDED.changed_at`

	_, err := s.pool.Exec(ctx, statement,
		revision.TenantID, revision.Revision, revision.ChangedAt)
	if err != nil {
		return fmt.Errorf("postgresstore: saving a permission revision: %w", err)
	}
	return nil
}

// PermissionRevision reads a tenant's current revision.
func (s *Store) PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	const query = `
		SELECT revision, changed_at FROM permission_revision WHERE tenant_id = $1`

	revision := assignmentstore.PermissionRevision{TenantID: tenantID}
	err := s.pool.QueryRow(ctx, query, tenantID).Scan(&revision.Revision, &revision.ChangedAt)
	if isNoRows(err) {
		return assignmentstore.PermissionRevision{}, false, nil
	}
	if err != nil {
		return assignmentstore.PermissionRevision{}, false, fmt.Errorf("postgresstore: reading a permission revision: %w", err)
	}
	return revision, true, nil
}

// AppendAuditEvent appends one audit record. Audit history is append-only
// (§8.2), so there is deliberately no update path.
func (s *Store) AppendAuditEvent(ctx context.Context, event assignmentstore.AuditEvent) error {
	const statement = `
		INSERT INTO permission_audit_event (
			event_id, actor_id, operation, target_type, before_json, after_json,
			tenant_id, hospital_id, correlation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err := s.pool.Exec(ctx, statement,
		event.EventID, event.ActorID, event.Operation, event.TargetType,
		event.BeforeJSON, event.AfterJSON, event.TenantID, event.HospitalID,
		event.CorrelationID, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgresstore: appending an audit event: %w", err)
	}
	return nil
}

// AuditEvent reads one audit record.
func (s *Store) AuditEvent(ctx context.Context, eventID string) (assignmentstore.AuditEvent, bool, error) {
	const query = `
		SELECT actor_id, operation, target_type, before_json, after_json,
		       tenant_id, hospital_id, correlation_id, created_at
		FROM permission_audit_event WHERE event_id = $1`

	event := assignmentstore.AuditEvent{EventID: eventID}
	err := s.pool.QueryRow(ctx, query, eventID).Scan(
		&event.ActorID, &event.Operation, &event.TargetType,
		&event.BeforeJSON, &event.AfterJSON, &event.TenantID,
		&event.HospitalID, &event.CorrelationID, &event.CreatedAt)
	if isNoRows(err) {
		return assignmentstore.AuditEvent{}, false, nil
	}
	if err != nil {
		return assignmentstore.AuditEvent{}, false, fmt.Errorf("postgresstore: reading an audit event: %w", err)
	}
	return event, true, nil
}

// AppendOutboxEvent appends one outbox row.
func (s *Store) AppendOutboxEvent(ctx context.Context, event assignmentstore.OutboxEvent) error {
	const statement = `
		INSERT INTO outbox_event (
			event_id, aggregate_key, event_type, payload, created_at, published_at)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := s.pool.Exec(ctx, statement,
		event.EventID, event.AggregateKey, event.EventType,
		event.Payload, event.CreatedAt, event.PublishedAt)
	if err != nil {
		return fmt.Errorf("postgresstore: appending an outbox event: %w", err)
	}
	return nil
}

// OutboxEvent reads one outbox row.
func (s *Store) OutboxEvent(ctx context.Context, eventID string) (assignmentstore.OutboxEvent, bool, error) {
	const query = `
		SELECT aggregate_key, event_type, payload, created_at, published_at
		FROM outbox_event WHERE event_id = $1`

	event := assignmentstore.OutboxEvent{EventID: eventID}
	var publishedAt *time.Time
	err := s.pool.QueryRow(ctx, query, eventID).Scan(
		&event.AggregateKey, &event.EventType, &event.Payload,
		&event.CreatedAt, &publishedAt)
	if isNoRows(err) {
		return assignmentstore.OutboxEvent{}, false, nil
	}
	if err != nil {
		return assignmentstore.OutboxEvent{}, false, fmt.Errorf("postgresstore: reading an outbox event: %w", err)
	}
	event.PublishedAt = publishedAt
	return event, true, nil
}

// UnpublishedOutboxEvents reads up to limit unpublished rows, oldest first.
func (s *Store) UnpublishedOutboxEvents(ctx context.Context, limit int) ([]assignmentstore.OutboxEvent, error) {
	const query = `
		SELECT event_id, aggregate_key, event_type, payload, created_at
		FROM outbox_event
		WHERE published_at IS NULL
		ORDER BY created_at, event_id
		LIMIT $1`

	rows, err := s.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: reading unpublished outbox events: %w", err)
	}
	defer rows.Close()

	var events []assignmentstore.OutboxEvent
	for rows.Next() {
		var event assignmentstore.OutboxEvent
		if err := rows.Scan(&event.EventID, &event.AggregateKey, &event.EventType,
			&event.Payload, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgresstore: scanning an outbox event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgresstore: reading unpublished outbox events: %w", err)
	}
	return events, nil
}

// MarkOutboxEventPublished records that an outbox row was published.
func (s *Store) MarkOutboxEventPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	const statement = `UPDATE outbox_event SET published_at = $2 WHERE event_id = $1`
	if _, err := s.pool.Exec(ctx, statement, eventID, publishedAt); err != nil {
		return fmt.Errorf("postgresstore: marking an outbox event published: %w", err)
	}
	return nil
}

// SaveRoleMatrix atomically writes one role's permission slice (§9.4,
// §10.1, §16.1).
//
// The tenant's permission_revision row is seeded first, if absent, so the
// lock below always has a row to take: without that seed, a genuinely
// concurrent first write for a brand-new tenant would have nothing to
// serialize it against its sibling. SELECT ... FOR UPDATE then blocks a
// second concurrent writer until the first commits or rolls back; the second
// writer's subsequent read sees whatever the first one left behind, which is
// what turns a stale ExpectedRevision into a clean conflict rather than a
// race.
func (s *Store) SaveRoleMatrix(ctx context.Context, write assignmentstore.RoleMatrixWrite) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgresstore: beginning the role matrix transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO permission_revision (tenant_id, revision, changed_at)
		VALUES ($1, 0, $2)
		ON CONFLICT (tenant_id) DO NOTHING`,
		write.TenantID, write.Audit.CreatedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: seeding the permission revision: %w", err)
	}

	var current int64
	if err := tx.QueryRow(ctx, `
		SELECT revision FROM permission_revision WHERE tenant_id = $1 FOR UPDATE`,
		write.TenantID).Scan(&current); err != nil {
		return 0, fmt.Errorf("postgresstore: locking the permission revision: %w", err)
	}
	if current != write.ExpectedRevision {
		return 0, assignmentstore.ErrRevisionConflict
	}

	newRevision := current + 1
	for _, permission := range write.Permissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permission (
				tenant_id, role_external_id, resource_key, action_key,
				enabled, valid_from, valid_until, revision)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (tenant_id, role_external_id, resource_key, action_key)
			DO UPDATE SET
				enabled = EXCLUDED.enabled,
				valid_from = EXCLUDED.valid_from,
				valid_until = EXCLUDED.valid_until,
				revision = EXCLUDED.revision`,
			write.TenantID, write.RoleExternalID, permission.ResourceKey, permission.ActionKey,
			permission.Enabled, permission.ValidFrom, endOrNull(permission.ValidUntil), newRevision); err != nil {
			return 0, fmt.Errorf("postgresstore: writing a role permission: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO permission_audit_event (
			event_id, actor_id, operation, target_type, before_json, after_json,
			tenant_id, hospital_id, correlation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		write.Audit.EventID, write.Audit.ActorID, write.Audit.Operation, write.Audit.TargetType,
		write.Audit.BeforeJSON, write.Audit.AfterJSON, write.TenantID, write.Audit.HospitalID,
		write.Audit.CorrelationID, write.Audit.CreatedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: appending the audit event: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_event (
			event_id, aggregate_key, event_type, payload, created_at, published_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		write.Outbox.EventID, write.Outbox.AggregateKey, write.Outbox.EventType,
		write.Outbox.Payload, write.Outbox.CreatedAt, write.Outbox.PublishedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: appending the outbox event: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE permission_revision SET revision = $2, changed_at = $3 WHERE tenant_id = $1`,
		write.TenantID, newRevision, write.Audit.CreatedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: advancing the permission revision: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgresstore: committing the role matrix transaction: %w", err)
	}
	return newRevision, nil
}

// SaveUserOverrideWrite atomically applies one tri-state change to a user
// override (§9.3, §9.4, §10.1). See SaveRoleMatrix for why the revision row
// is seeded before it is locked.
func (s *Store) SaveUserOverrideWrite(ctx context.Context, write assignmentstore.UserOverrideWrite) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgresstore: beginning the user override transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO permission_revision (tenant_id, revision, changed_at)
		VALUES ($1, 0, $2)
		ON CONFLICT (tenant_id) DO NOTHING`,
		write.Key.TenantID, write.Audit.CreatedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: seeding the permission revision: %w", err)
	}

	var current int64
	if err := tx.QueryRow(ctx, `
		SELECT revision FROM permission_revision WHERE tenant_id = $1 FOR UPDATE`,
		write.Key.TenantID).Scan(&current); err != nil {
		return 0, fmt.Errorf("postgresstore: locking the permission revision: %w", err)
	}
	if current != write.ExpectedRevision {
		return 0, assignmentstore.ErrRevisionConflict
	}

	newRevision := current + 1
	key := assignmentstore.NormalizeOverrideKey(write.Key)
	if write.Effect == "" {
		// INHERIT: the override row is cleared rather than upserted (§8.3).
		if _, err := tx.Exec(ctx, `
			DELETE FROM user_permission_override
			WHERE tenant_id = $1 AND hospital_id = $2 AND user_external_id = $3
			  AND resource_key = $4 AND action_key = $5 AND resource_instance_id = $6`,
			key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
			key.ActionKey, key.ResourceInstanceID); err != nil {
			return 0, fmt.Errorf("postgresstore: clearing a user override: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_permission_override (
				tenant_id, hospital_id, user_external_id, resource_key, action_key,
				resource_instance_id, effect, enabled, valid_from, valid_until, revision, reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (tenant_id, hospital_id, user_external_id, resource_key,
				action_key, resource_instance_id)
			DO UPDATE SET
				effect = EXCLUDED.effect,
				enabled = EXCLUDED.enabled,
				valid_from = EXCLUDED.valid_from,
				valid_until = EXCLUDED.valid_until,
				revision = EXCLUDED.revision,
				reason = EXCLUDED.reason`,
			key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
			key.ActionKey, key.ResourceInstanceID, string(write.Effect), true,
			write.ValidFrom, endOrNull(write.ValidUntil), newRevision, nullableString(write.Reason)); err != nil {
			return 0, fmt.Errorf("postgresstore: writing a user override: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO permission_audit_event (
			event_id, actor_id, operation, target_type, before_json, after_json,
			tenant_id, hospital_id, correlation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		write.Audit.EventID, write.Audit.ActorID, write.Audit.Operation, write.Audit.TargetType,
		write.Audit.BeforeJSON, write.Audit.AfterJSON, key.TenantID, key.HospitalID,
		write.Audit.CorrelationID, write.Audit.CreatedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: appending the audit event: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_event (
			event_id, aggregate_key, event_type, payload, created_at, published_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		write.Outbox.EventID, write.Outbox.AggregateKey, write.Outbox.EventType,
		write.Outbox.Payload, write.Outbox.CreatedAt, write.Outbox.PublishedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: appending the outbox event: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE permission_revision SET revision = $2, changed_at = $3 WHERE tenant_id = $1`,
		key.TenantID, newRevision, write.Audit.CreatedAt); err != nil {
		return 0, fmt.Errorf("postgresstore: advancing the permission revision: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgresstore: committing the user override transaction: %w", err)
	}
	return newRevision, nil
}

// SaveCapability inserts or updates one capability definition.
func (s *Store) SaveCapability(ctx context.Context, capability assignmentstore.Capability) error {
	const statement = `
		INSERT INTO ui_capability_definition (
			capability_key, module_key, context_type, expression_json,
			catalog_revision, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (capability_key) DO UPDATE SET
			module_key = EXCLUDED.module_key,
			context_type = EXCLUDED.context_type,
			expression_json = EXCLUDED.expression_json,
			catalog_revision = EXCLUDED.catalog_revision,
			enabled = EXCLUDED.enabled`

	_, err := s.pool.Exec(ctx, statement,
		capability.CapabilityKey, capability.ModuleKey, capability.ContextType,
		capability.ExpressionJSON, capability.CatalogRevision, capability.Enabled)
	if err != nil {
		return fmt.Errorf("postgresstore: saving a capability: %w", err)
	}
	return nil
}

// Capability reads one capability definition.
func (s *Store) Capability(ctx context.Context, capabilityKey string) (assignmentstore.Capability, bool, error) {
	const query = `
		SELECT module_key, context_type, expression_json, catalog_revision, enabled
		FROM ui_capability_definition WHERE capability_key = $1`

	capability := assignmentstore.Capability{CapabilityKey: capabilityKey}
	err := s.pool.QueryRow(ctx, query, capabilityKey).Scan(
		&capability.ModuleKey, &capability.ContextType, &capability.ExpressionJSON,
		&capability.CatalogRevision, &capability.Enabled)
	if isNoRows(err) {
		return assignmentstore.Capability{}, false, nil
	}
	if err != nil {
		return assignmentstore.Capability{}, false, fmt.Errorf("postgresstore: reading a capability: %w", err)
	}
	return capability, true, nil
}

// SaveInstallationConfig inserts or updates the installation configuration.
func (s *Store) SaveInstallationConfig(ctx context.Context, config assignmentstore.InstallationConfig) error {
	const statement = `
		INSERT INTO installation_config (
			installation_id, idp_type, idp_config, active_root_tag)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (installation_id) DO UPDATE SET
			idp_type = EXCLUDED.idp_type,
			idp_config = EXCLUDED.idp_config,
			active_root_tag = EXCLUDED.active_root_tag`

	_, err := s.pool.Exec(ctx, statement,
		config.InstallationID, config.IDPType, config.IDPConfigJSON, config.ActiveRootTag)
	if err != nil {
		return fmt.Errorf("postgresstore: saving the installation config: %w", err)
	}
	return nil
}

// InstallationConfig reads the installation configuration.
func (s *Store) InstallationConfig(ctx context.Context, installationID string) (assignmentstore.InstallationConfig, bool, error) {
	const query = `
		SELECT idp_type, idp_config, active_root_tag
		FROM installation_config WHERE installation_id = $1`

	config := assignmentstore.InstallationConfig{InstallationID: installationID}
	err := s.pool.QueryRow(ctx, query, installationID).Scan(
		&config.IDPType, &config.IDPConfigJSON, &config.ActiveRootTag)
	if isNoRows(err) {
		return assignmentstore.InstallationConfig{}, false, nil
	}
	if err != nil {
		return assignmentstore.InstallationConfig{}, false, fmt.Errorf("postgresstore: reading the installation config: %w", err)
	}
	return config, true, nil
}

// SaveResource inserts or updates one business resource.
func (s *Store) SaveResource(ctx context.Context, resource assignmentstore.Resource) error {
	const statement = `
		INSERT INTO fhir_resource (
			resource_type, resource_id, tenant_id, hospital_id,
			status, department, sensitivity, payload, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (resource_type, resource_id) DO UPDATE SET
			tenant_id = EXCLUDED.tenant_id,
			hospital_id = EXCLUDED.hospital_id,
			status = EXCLUDED.status,
			department = EXCLUDED.department,
			sensitivity = EXCLUDED.sensitivity,
			payload = EXCLUDED.payload,
			updated_at = EXCLUDED.updated_at`

	_, err := s.pool.Exec(ctx, statement,
		resource.ResourceType, resource.ResourceID, resource.TenantID, resource.HospitalID,
		resource.Status, resource.Department, resource.Sensitivity,
		resource.PayloadJSON, resource.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgresstore: saving a resource: %w", err)
	}
	return nil
}

// Resource reads one business resource.
//
// department and sensitivity are read as nullable even though this engine can
// store an empty string faithfully. Oracle cannot, so a row written there arrives
// as NULL, and both adapters must hand callers the same empty string rather than
// leaving them to discover which engine wrote the row.
func (s *Store) Resource(ctx context.Context, resourceType, resourceID string) (assignmentstore.Resource, bool, error) {
	const query = `
		SELECT tenant_id, hospital_id, status, department, sensitivity, payload, updated_at
		FROM fhir_resource WHERE resource_type = $1 AND resource_id = $2`

	resource := assignmentstore.Resource{ResourceType: resourceType, ResourceID: resourceID}
	var department, sensitivity *string
	err := s.pool.QueryRow(ctx, query, resourceType, resourceID).Scan(
		&resource.TenantID, &resource.HospitalID, &resource.Status,
		&department, &sensitivity, &resource.PayloadJSON, &resource.UpdatedAt)
	if isNoRows(err) {
		return assignmentstore.Resource{}, false, nil
	}
	if err != nil {
		return assignmentstore.Resource{}, false, fmt.Errorf("postgresstore: reading a resource: %w", err)
	}
	if department != nil {
		resource.Department = *department
	}
	if sensitivity != nil {
		resource.Sensitivity = *sensitivity
	}
	return resource, true, nil
}

// DeleteResource removes one instance. Deleting a row that is not there is
// not an error.
func (s *Store) DeleteResource(ctx context.Context, resourceType, resourceID string) error {
	const statement = `DELETE FROM fhir_resource WHERE resource_type = $1 AND resource_id = $2`
	if _, err := s.pool.Exec(ctx, statement, resourceType, resourceID); err != nil {
		return fmt.Errorf("postgresstore: deleting a resource: %w", err)
	}
	return nil
}

// ListResources pages through instances of one resource type within one
// tenant and hospital, ordered by resource_id for stable pagination.
func (s *Store) ListResources(ctx context.Context, query assignmentstore.ListResourcesQuery) ([]assignmentstore.Resource, int, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = assignmentstore.DefaultListLimit
	}

	const countQuery = `
		SELECT count(*) FROM fhir_resource
		WHERE resource_type = $1 AND tenant_id = $2 AND hospital_id = $3`
	var total int
	if err := s.pool.QueryRow(ctx, countQuery, query.ResourceType, query.TenantID, query.HospitalID).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgresstore: counting resources: %w", err)
	}

	const pageQuery = `
		SELECT resource_id, status, department, sensitivity, payload, updated_at
		FROM fhir_resource
		WHERE resource_type = $1 AND tenant_id = $2 AND hospital_id = $3
		ORDER BY resource_id
		LIMIT $4 OFFSET $5`
	rows, err := s.pool.Query(ctx, pageQuery,
		query.ResourceType, query.TenantID, query.HospitalID, limit, query.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("postgresstore: listing resources: %w", err)
	}
	defer rows.Close()

	var page []assignmentstore.Resource
	for rows.Next() {
		resource := assignmentstore.Resource{
			ResourceType: query.ResourceType,
			TenantID:     query.TenantID,
			HospitalID:   query.HospitalID,
		}
		var department, sensitivity *string
		if err := rows.Scan(&resource.ResourceID, &resource.Status,
			&department, &sensitivity, &resource.PayloadJSON, &resource.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("postgresstore: scanning a resource: %w", err)
		}
		if department != nil {
			resource.Department = *department
		}
		if sensitivity != nil {
			resource.Sensitivity = *sensitivity
		}
		page = append(page, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgresstore: listing resources: %w", err)
	}
	return page, total, nil
}

// Truncate empties the named tables.
func (s *Store) Truncate(ctx context.Context, tables ...string) error {
	for _, table := range tables {
		if err := assignmentstore.ValidateTableName(table); err != nil {
			return fmt.Errorf("postgresstore: %w", err)
		}
		if _, err := s.pool.Exec(ctx, "TRUNCATE TABLE "+table); err != nil {
			return fmt.Errorf("postgresstore: truncating %s: %w", table, err)
		}
	}
	return nil
}
