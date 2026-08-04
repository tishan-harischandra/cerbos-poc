// Package oraclestore is the Oracle adapter for the assignmentstore port.
//
// The driver is go-ora, which speaks Oracle's wire protocol in pure Go. That is
// a deliberate constraint: an ODPI-based driver would require Oracle Instant
// Client inside every image that touches the database.
//
// Three Oracle behaviours are absorbed here rather than exposed to callers:
// there is no native boolean, so a generic BOOLEAN column arrives as NUMBER(1);
// the empty string is stored as NULL; and MERGE takes the place of an upsert.
package oraclestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/sijms/go-ora/v2" // registers the pure-Go "oracle" driver
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Store is the Oracle implementation of assignmentstore.Store.
type Store struct {
	db *sql.DB
}

// Open connects to Oracle. The DSN is an oracle://user:password@host:port/service URL.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("oraclestore: no DSN configured")
	}
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, fmt.Errorf("oraclestore: opening: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("oraclestore: connecting: %w", err)
	}
	return &Store{db: db}, nil
}

// Ping reports whether Oracle is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close releases the pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// Schema reports the shape of the migrated schema.
func (s *Store) Schema() assignmentstore.SchemaInspector {
	return inspector{db: s.db}
}

// SaveRolePermission inserts or updates one role permission on its §8.2 key.
//
// MERGE rather than an upsert clause: Oracle has no ON CONFLICT.
func (s *Store) SaveRolePermission(ctx context.Context, permission assignmentstore.RolePermission) error {
	const statement = `
		MERGE INTO role_permission t
		USING (SELECT :1 AS tenant_id, :2 AS role_external_id,
		              :3 AS resource_key, :4 AS action_key FROM dual) s
		ON (t.tenant_id = s.tenant_id AND t.role_external_id = s.role_external_id
		    AND t.resource_key = s.resource_key AND t.action_key = s.action_key)
		WHEN MATCHED THEN UPDATE SET
			enabled = :5, valid_from = :6, valid_until = :7, revision = :8
		WHEN NOT MATCHED THEN INSERT (
			tenant_id, role_external_id, resource_key, action_key,
			enabled, valid_from, valid_until, revision)
		VALUES (s.tenant_id, s.role_external_id, s.resource_key, s.action_key,
			:9, :10, :11, :12)`

	key := permission.Key
	enabled := boolToNumber(permission.Enabled)
	_, err := s.db.ExecContext(ctx, statement,
		key.TenantID, key.RoleExternalID, key.ResourceKey, key.ActionKey,
		enabled, permission.ValidFrom, permission.ValidUntil, permission.Revision,
		enabled, permission.ValidFrom, permission.ValidUntil, permission.Revision)
	if err != nil {
		return fmt.Errorf("oraclestore: saving a role permission: %w", err)
	}
	return nil
}

// RolePermission reads one role permission by its §8.2 key.
func (s *Store) RolePermission(ctx context.Context, key assignmentstore.RolePermissionKey) (assignmentstore.RolePermission, bool, error) {
	const query = `
		SELECT enabled, valid_from, valid_until, revision
		FROM role_permission
		WHERE tenant_id = :1 AND role_external_id = :2
		  AND resource_key = :3 AND action_key = :4`

	permission := assignmentstore.RolePermission{Key: key}
	var enabled int64
	err := s.db.QueryRowContext(ctx, query,
		key.TenantID, key.RoleExternalID, key.ResourceKey, key.ActionKey).
		Scan(&enabled, &permission.ValidFrom, &permission.ValidUntil, &permission.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.RolePermission{}, false, nil
	}
	if err != nil {
		return assignmentstore.RolePermission{}, false, fmt.Errorf("oraclestore: reading a role permission: %w", err)
	}
	permission.Enabled = enabled != 0
	return permission, true, nil
}

// SaveUserOverride inserts or updates one override on its §8.2 key.
func (s *Store) SaveUserOverride(ctx context.Context, override assignmentstore.UserOverride) error {
	const statement = `
		MERGE INTO user_permission_override t
		USING (SELECT :1 AS tenant_id, :2 AS hospital_id, :3 AS user_external_id,
		              :4 AS resource_key, :5 AS action_key,
		              :6 AS resource_instance_id FROM dual) s
		ON (t.tenant_id = s.tenant_id AND t.hospital_id = s.hospital_id
		    AND t.user_external_id = s.user_external_id
		    AND t.resource_key = s.resource_key AND t.action_key = s.action_key
		    AND t.resource_instance_id = s.resource_instance_id)
		WHEN MATCHED THEN UPDATE SET
			effect = :7, enabled = :8, valid_from = :9, valid_until = :10, revision = :11
		WHEN NOT MATCHED THEN INSERT (
			tenant_id, hospital_id, user_external_id, resource_key, action_key,
			resource_instance_id, effect, enabled, valid_from, valid_until, revision)
		VALUES (s.tenant_id, s.hospital_id, s.user_external_id, s.resource_key,
			s.action_key, s.resource_instance_id, :12, :13, :14, :15, :16)`

	key := assignmentstore.NormalizeOverrideKey(override.Key)
	enabled := boolToNumber(override.Enabled)
	_, err := s.db.ExecContext(ctx, statement,
		key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
		key.ActionKey, key.ResourceInstanceID,
		string(override.Effect), enabled, override.ValidFrom, override.ValidUntil, override.Revision,
		string(override.Effect), enabled, override.ValidFrom, override.ValidUntil, override.Revision)
	if err != nil {
		return fmt.Errorf("oraclestore: saving a user override: %w", err)
	}
	return nil
}

// UserOverride reads one override by its §8.2 key.
func (s *Store) UserOverride(ctx context.Context, key assignmentstore.UserOverrideKey) (assignmentstore.UserOverride, bool, error) {
	const query = `
		SELECT effect, enabled, valid_from, valid_until, revision
		FROM user_permission_override
		WHERE tenant_id = :1 AND hospital_id = :2 AND user_external_id = :3
		  AND resource_key = :4 AND action_key = :5 AND resource_instance_id = :6`

	key = assignmentstore.NormalizeOverrideKey(key)
	override := assignmentstore.UserOverride{Key: key}
	var effect string
	var enabled int64
	err := s.db.QueryRowContext(ctx, query,
		key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
		key.ActionKey, key.ResourceInstanceID).
		Scan(&effect, &enabled, &override.ValidFrom, &override.ValidUntil, &override.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.UserOverride{}, false, nil
	}
	if err != nil {
		return assignmentstore.UserOverride{}, false, fmt.Errorf("oraclestore: reading a user override: %w", err)
	}
	override.Effect = assignmentstore.OverrideEffect(effect)
	override.Enabled = enabled != 0
	return override, true, nil
}

// AppendAuditEvent appends one audit record.
func (s *Store) AppendAuditEvent(ctx context.Context, event assignmentstore.AuditEvent) error {
	const statement = `
		INSERT INTO permission_audit_event (
			event_id, actor_id, operation, target_type, before_json, after_json,
			tenant_id, hospital_id, correlation_id, created_at)
		VALUES (:1, :2, :3, :4, :5, :6, :7, :8, :9, :10)`

	_, err := s.db.ExecContext(ctx, statement,
		event.EventID, event.ActorID, event.Operation, event.TargetType,
		event.BeforeJSON, event.AfterJSON, event.TenantID, event.HospitalID,
		event.CorrelationID, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("oraclestore: appending an audit event: %w", err)
	}
	return nil
}

// AuditEvent reads one audit record.
func (s *Store) AuditEvent(ctx context.Context, eventID string) (assignmentstore.AuditEvent, bool, error) {
	const query = `
		SELECT actor_id, operation, target_type, before_json, after_json,
		       tenant_id, hospital_id, correlation_id, created_at
		FROM permission_audit_event WHERE event_id = :1`

	event := assignmentstore.AuditEvent{EventID: eventID}
	// The JSON columns are CLOBs. go-ora returns them as string when scanned
	// into one, so no LOB handling leaks out of this package.
	err := s.db.QueryRowContext(ctx, query, eventID).Scan(
		&event.ActorID, &event.Operation, &event.TargetType,
		&event.BeforeJSON, &event.AfterJSON, &event.TenantID,
		&event.HospitalID, &event.CorrelationID, &event.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.AuditEvent{}, false, nil
	}
	if err != nil {
		return assignmentstore.AuditEvent{}, false, fmt.Errorf("oraclestore: reading an audit event: %w", err)
	}
	return event, true, nil
}

// AppendOutboxEvent appends one outbox row.
func (s *Store) AppendOutboxEvent(ctx context.Context, event assignmentstore.OutboxEvent) error {
	const statement = `
		INSERT INTO outbox_event (
			event_id, aggregate_key, event_type, payload, created_at, published_at)
		VALUES (:1, :2, :3, :4, :5, :6)`

	var publishedAt any
	if event.PublishedAt != nil {
		publishedAt = *event.PublishedAt
	}
	_, err := s.db.ExecContext(ctx, statement,
		event.EventID, event.AggregateKey, event.EventType,
		event.Payload, event.CreatedAt, publishedAt)
	if err != nil {
		return fmt.Errorf("oraclestore: appending an outbox event: %w", err)
	}
	return nil
}

// OutboxEvent reads one outbox row.
func (s *Store) OutboxEvent(ctx context.Context, eventID string) (assignmentstore.OutboxEvent, bool, error) {
	const query = `
		SELECT aggregate_key, event_type, payload, created_at, published_at
		FROM outbox_event WHERE event_id = :1`

	event := assignmentstore.OutboxEvent{EventID: eventID}
	var publishedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, eventID).Scan(
		&event.AggregateKey, &event.EventType, &event.Payload,
		&event.CreatedAt, &publishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.OutboxEvent{}, false, nil
	}
	if err != nil {
		return assignmentstore.OutboxEvent{}, false, fmt.Errorf("oraclestore: reading an outbox event: %w", err)
	}
	if publishedAt.Valid {
		published := publishedAt.Time
		event.PublishedAt = &published
	}
	return event, true, nil
}

// SaveCapability inserts or updates one capability definition.
func (s *Store) SaveCapability(ctx context.Context, capability assignmentstore.Capability) error {
	const statement = `
		MERGE INTO ui_capability_definition t
		USING (SELECT :1 AS capability_key FROM dual) s
		ON (t.capability_key = s.capability_key)
		WHEN MATCHED THEN UPDATE SET
			module_key = :2, context_type = :3, expression_json = :4,
			catalog_revision = :5, enabled = :6
		WHEN NOT MATCHED THEN INSERT (
			capability_key, module_key, context_type, expression_json,
			catalog_revision, enabled)
		VALUES (s.capability_key, :7, :8, :9, :10, :11)`

	enabled := boolToNumber(capability.Enabled)
	_, err := s.db.ExecContext(ctx, statement,
		capability.CapabilityKey,
		capability.ModuleKey, capability.ContextType, capability.ExpressionJSON,
		capability.CatalogRevision, enabled,
		capability.ModuleKey, capability.ContextType, capability.ExpressionJSON,
		capability.CatalogRevision, enabled)
	if err != nil {
		return fmt.Errorf("oraclestore: saving a capability: %w", err)
	}
	return nil
}

// Capability reads one capability definition.
func (s *Store) Capability(ctx context.Context, capabilityKey string) (assignmentstore.Capability, bool, error) {
	const query = `
		SELECT module_key, context_type, expression_json, catalog_revision, enabled
		FROM ui_capability_definition WHERE capability_key = :1`

	capability := assignmentstore.Capability{CapabilityKey: capabilityKey}
	var enabled int64
	err := s.db.QueryRowContext(ctx, query, capabilityKey).Scan(
		&capability.ModuleKey, &capability.ContextType, &capability.ExpressionJSON,
		&capability.CatalogRevision, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.Capability{}, false, nil
	}
	if err != nil {
		return assignmentstore.Capability{}, false, fmt.Errorf("oraclestore: reading a capability: %w", err)
	}
	capability.Enabled = enabled != 0
	return capability, true, nil
}

// Truncate empties the named tables.
func (s *Store) Truncate(ctx context.Context, tables ...string) error {
	for _, table := range tables {
		if err := assignmentstore.ValidateTableName(table); err != nil {
			return fmt.Errorf("oraclestore: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, "TRUNCATE TABLE "+table); err != nil {
			return fmt.Errorf("oraclestore: truncating %s: %w", table, err)
		}
	}
	return nil
}

// boolToNumber maps Go's bool onto the NUMBER(1) that a generic Liquibase
// BOOLEAN column becomes on Oracle.
func boolToNumber(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
