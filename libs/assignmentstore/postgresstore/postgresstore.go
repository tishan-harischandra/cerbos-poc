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
		permission.Enabled, permission.ValidFrom, permission.ValidUntil, permission.Revision)
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
	err := s.pool.QueryRow(ctx, query,
		key.TenantID, key.RoleExternalID, key.ResourceKey, key.ActionKey).
		Scan(&permission.Enabled, &permission.ValidFrom, &permission.ValidUntil, &permission.Revision)
	if isNoRows(err) {
		return assignmentstore.RolePermission{}, false, nil
	}
	if err != nil {
		return assignmentstore.RolePermission{}, false, fmt.Errorf("postgresstore: reading a role permission: %w", err)
	}
	return permission, true, nil
}

// SaveUserOverride inserts or updates one override on its §8.2 key.
func (s *Store) SaveUserOverride(ctx context.Context, override assignmentstore.UserOverride) error {
	const statement = `
		INSERT INTO user_permission_override (
			tenant_id, hospital_id, user_external_id, resource_key, action_key,
			resource_instance_id, effect, enabled, valid_from, valid_until, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (tenant_id, hospital_id, user_external_id, resource_key,
			action_key, resource_instance_id)
		DO UPDATE SET
			effect = EXCLUDED.effect,
			enabled = EXCLUDED.enabled,
			valid_from = EXCLUDED.valid_from,
			valid_until = EXCLUDED.valid_until,
			revision = EXCLUDED.revision`

	key := assignmentstore.NormalizeOverrideKey(override.Key)
	_, err := s.pool.Exec(ctx, statement,
		key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
		key.ActionKey, key.ResourceInstanceID, string(override.Effect),
		override.Enabled, override.ValidFrom, override.ValidUntil, override.Revision)
	if err != nil {
		return fmt.Errorf("postgresstore: saving a user override: %w", err)
	}
	return nil
}

// UserOverride reads one override by its §8.2 key.
func (s *Store) UserOverride(ctx context.Context, key assignmentstore.UserOverrideKey) (assignmentstore.UserOverride, bool, error) {
	const query = `
		SELECT effect, enabled, valid_from, valid_until, revision
		FROM user_permission_override
		WHERE tenant_id = $1 AND hospital_id = $2 AND user_external_id = $3
		  AND resource_key = $4 AND action_key = $5 AND resource_instance_id = $6`

	key = assignmentstore.NormalizeOverrideKey(key)
	override := assignmentstore.UserOverride{Key: key}
	var effect string
	err := s.pool.QueryRow(ctx, query,
		key.TenantID, key.HospitalID, key.UserExternalID, key.ResourceKey,
		key.ActionKey, key.ResourceInstanceID).
		Scan(&effect, &override.Enabled, &override.ValidFrom, &override.ValidUntil, &override.Revision)
	if isNoRows(err) {
		return assignmentstore.UserOverride{}, false, nil
	}
	if err != nil {
		return assignmentstore.UserOverride{}, false, fmt.Errorf("postgresstore: reading a user override: %w", err)
	}
	override.Effect = assignmentstore.OverrideEffect(effect)
	return override, true, nil
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
