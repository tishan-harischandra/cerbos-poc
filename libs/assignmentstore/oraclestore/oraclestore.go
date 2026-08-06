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
	"strings"
	"time"

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
	validUntil := endOrNull(permission.ValidUntil)
	_, err := s.db.ExecContext(ctx, statement,
		key.TenantID, key.RoleExternalID, key.ResourceKey, key.ActionKey,
		enabled, permission.ValidFrom, validUntil, permission.Revision,
		enabled, permission.ValidFrom, validUntil, permission.Revision)
	if err != nil {
		return fmt.Errorf("oraclestore: saving a role permission: %w", err)
	}
	return nil
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

// RolePermission reads one role permission by its §8.2 key.
func (s *Store) RolePermission(ctx context.Context, key assignmentstore.RolePermissionKey) (assignmentstore.RolePermission, bool, error) {
	const query = `
		SELECT enabled, valid_from, valid_until, revision
		FROM role_permission
		WHERE tenant_id = :1 AND role_external_id = :2
		  AND resource_key = :3 AND action_key = :4`

	permission := assignmentstore.RolePermission{Key: key}
	var enabled int64
	var validUntil *time.Time
	err := s.db.QueryRowContext(ctx, query,
		key.TenantID, key.RoleExternalID, key.ResourceKey, key.ActionKey).
		Scan(&enabled, &permission.ValidFrom, &validUntil, &permission.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.RolePermission{}, false, nil
	}
	if err != nil {
		return assignmentstore.RolePermission{}, false, fmt.Errorf("oraclestore: reading a role permission: %w", err)
	}
	permission.Enabled = enabled != 0
	if validUntil != nil {
		permission.ValidUntil = *validUntil
	}
	return permission, true, nil
}

// ActiveRolePermissions reads the whole role matrix for a set of roles in one
// round trip.
//
// Oracle binds no array to an IN list without a schema-level collection type, so
// the placeholders are generated from the role count. They are positional binds,
// never interpolated values, so nothing a caller supplies reaches the statement
// text.
//
// Oracle caps an IN list at 1000 expressions (ORA-01795). §11.2 sizes a tenant
// at roughly 250 roles and the caller deduplicates the claim, so a real
// principal stays well under that. A deployment that ever approaches it needs
// chunked reads or a collection type rather than a larger statement, and would
// see a clear ORA-01795 rather than a wrong answer.
func (s *Store) ActiveRolePermissions(ctx context.Context, query assignmentstore.ActiveRolePermissionQuery) ([]assignmentstore.RolePermission, error) {
	if len(query.RoleExternalIDs) == 0 {
		return nil, nil
	}

	// The instant is bound twice, once per occurrence. The driver binds by
	// position, not by name, so a placeholder mentioned twice consumes two
	// arguments: reusing :3 for both ends of the window silently shifts every
	// later bind and the statement fails with ORA-01008. PostgreSQL is happy to
	// reuse $4, which is exactly why this only shows up on the second engine.
	arguments := []any{query.TenantID, query.ResourceKey, query.At, query.At}
	placeholders := make([]string, 0, len(query.RoleExternalIDs))
	for _, role := range query.RoleExternalIDs {
		arguments = append(arguments, role)
		placeholders = append(placeholders, fmt.Sprintf(":%d", len(arguments)))
	}

	statement := `
		SELECT role_external_id, action_key, enabled, valid_from, valid_until, revision
		FROM role_permission
		WHERE tenant_id = :1
		  AND resource_key = :2
		  AND valid_from <= :3
		  AND (valid_until IS NULL OR valid_until > :4)
		  AND role_external_id IN (` + strings.Join(placeholders, ", ") + `)`

	rows, err := s.db.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("oraclestore: reading the active role matrix: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var permissions []assignmentstore.RolePermission
	for rows.Next() {
		permission := assignmentstore.RolePermission{
			Key: assignmentstore.RolePermissionKey{
				TenantID:    query.TenantID,
				ResourceKey: query.ResourceKey,
			},
		}
		var enabled int64
		var validUntil *time.Time
		if err := rows.Scan(&permission.Key.RoleExternalID, &permission.Key.ActionKey,
			&enabled, &permission.ValidFrom, &validUntil, &permission.Revision); err != nil {
			return nil, fmt.Errorf("oraclestore: scanning a role permission: %w", err)
		}
		permission.Enabled = enabled != 0
		if validUntil != nil {
			permission.ValidUntil = *validUntil
		}
		permissions = append(permissions, permission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oraclestore: reading the active role matrix: %w", err)
	}
	return permissions, nil
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

// ActiveUserOverrides reads every override in force for one principal and one
// resource in one round trip, mirroring ActiveRolePermissions above.
//
// The instant is bound twice for the same reason ActiveRolePermissions binds
// it twice: Oracle's driver binds by position, and reusing a placeholder for
// both ends of the validity window would silently shift every later bind.
func (s *Store) ActiveUserOverrides(ctx context.Context, query assignmentstore.ActiveUserOverridesQuery) ([]assignmentstore.UserOverride, error) {
	instance := query.ResourceInstanceID
	if instance == "" {
		instance = assignmentstore.NoResourceInstance
	}

	const statement = `
		SELECT action_key, resource_instance_id, effect, enabled, valid_from, valid_until, revision
		FROM user_permission_override
		WHERE tenant_id = :1
		  AND hospital_id = :2
		  AND user_external_id = :3
		  AND resource_key = :4
		  AND valid_from <= :5
		  AND (valid_until IS NULL OR valid_until > :6)
		  AND resource_instance_id IN (:7, :8)`

	rows, err := s.db.QueryContext(ctx, statement,
		query.TenantID, query.HospitalID, query.UserExternalID, query.ResourceKey,
		query.At, query.At, assignmentstore.NoResourceInstance, instance)
	if err != nil {
		return nil, fmt.Errorf("oraclestore: reading the active user overrides: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var overrides []assignmentstore.UserOverride
	for rows.Next() {
		override := assignmentstore.UserOverride{
			Key: assignmentstore.UserOverrideKey{
				TenantID: query.TenantID, HospitalID: query.HospitalID,
				UserExternalID: query.UserExternalID, ResourceKey: query.ResourceKey,
			},
		}
		var effect string
		var enabled int64
		var validUntil *time.Time
		if err := rows.Scan(&override.Key.ActionKey, &override.Key.ResourceInstanceID,
			&effect, &enabled, &override.ValidFrom, &validUntil, &override.Revision); err != nil {
			return nil, fmt.Errorf("oraclestore: scanning a user override: %w", err)
		}
		override.Effect = assignmentstore.OverrideEffect(effect)
		override.Enabled = enabled != 0
		if validUntil != nil {
			override.ValidUntil = *validUntil
		}
		overrides = append(overrides, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oraclestore: reading the active user overrides: %w", err)
	}
	return overrides, nil
}

// SavePermissionRevision advances a tenant's revision in place.
func (s *Store) SavePermissionRevision(ctx context.Context, revision assignmentstore.PermissionRevision) error {
	const statement = `
		MERGE INTO permission_revision t
		USING (SELECT :1 AS tenant_id FROM dual) s
		ON (t.tenant_id = s.tenant_id)
		WHEN MATCHED THEN UPDATE SET revision = :2, changed_at = :3
		WHEN NOT MATCHED THEN INSERT (tenant_id, revision, changed_at)
		VALUES (s.tenant_id, :4, :5)`

	_, err := s.db.ExecContext(ctx, statement,
		revision.TenantID,
		revision.Revision, revision.ChangedAt,
		revision.Revision, revision.ChangedAt)
	if err != nil {
		return fmt.Errorf("oraclestore: saving a permission revision: %w", err)
	}
	return nil
}

// PermissionRevision reads a tenant's current revision.
func (s *Store) PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	const query = `
		SELECT revision, changed_at FROM permission_revision WHERE tenant_id = :1`

	revision := assignmentstore.PermissionRevision{TenantID: tenantID}
	err := s.db.QueryRowContext(ctx, query, tenantID).
		Scan(&revision.Revision, &revision.ChangedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.PermissionRevision{}, false, nil
	}
	if err != nil {
		return assignmentstore.PermissionRevision{}, false, fmt.Errorf("oraclestore: reading a permission revision: %w", err)
	}
	return revision, true, nil
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

// SaveInstallationConfig inserts or updates the installation configuration.
func (s *Store) SaveInstallationConfig(ctx context.Context, config assignmentstore.InstallationConfig) error {
	const statement = `
		MERGE INTO installation_config t
		USING (SELECT :1 AS installation_id FROM dual) s
		ON (t.installation_id = s.installation_id)
		WHEN MATCHED THEN UPDATE SET
			idp_type = :2, idp_config = :3, active_root_tag = :4
		WHEN NOT MATCHED THEN INSERT (
			installation_id, idp_type, idp_config, active_root_tag)
		VALUES (s.installation_id, :5, :6, :7)`

	_, err := s.db.ExecContext(ctx, statement,
		config.InstallationID,
		config.IDPType, config.IDPConfigJSON, config.ActiveRootTag,
		config.IDPType, config.IDPConfigJSON, config.ActiveRootTag)
	if err != nil {
		return fmt.Errorf("oraclestore: saving the installation config: %w", err)
	}
	return nil
}

// InstallationConfig reads the installation configuration.
func (s *Store) InstallationConfig(ctx context.Context, installationID string) (assignmentstore.InstallationConfig, bool, error) {
	const query = `
		SELECT idp_type, idp_config, active_root_tag
		FROM installation_config WHERE installation_id = :1`

	config := assignmentstore.InstallationConfig{InstallationID: installationID}
	err := s.db.QueryRowContext(ctx, query, installationID).Scan(
		&config.IDPType, &config.IDPConfigJSON, &config.ActiveRootTag)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.InstallationConfig{}, false, nil
	}
	if err != nil {
		return assignmentstore.InstallationConfig{}, false, fmt.Errorf("oraclestore: reading the installation config: %w", err)
	}
	return config, true, nil
}

// SaveResource inserts or updates one business resource.
func (s *Store) SaveResource(ctx context.Context, resource assignmentstore.Resource) error {
	const statement = `
		MERGE INTO fhir_resource t
		USING (SELECT :1 AS resource_type, :2 AS resource_id FROM dual) s
		ON (t.resource_type = s.resource_type AND t.resource_id = s.resource_id)
		WHEN MATCHED THEN UPDATE SET
			tenant_id = :3, hospital_id = :4, status = :5, department = :6,
			sensitivity = :7, payload = :8, updated_at = :9
		WHEN NOT MATCHED THEN INSERT (
			resource_type, resource_id, tenant_id, hospital_id,
			status, department, sensitivity, payload, updated_at)
		VALUES (s.resource_type, s.resource_id, :10, :11, :12, :13, :14, :15, :16)`

	_, err := s.db.ExecContext(ctx, statement,
		resource.ResourceType, resource.ResourceID,
		resource.TenantID, resource.HospitalID, resource.Status, resource.Department,
		resource.Sensitivity, resource.PayloadJSON, resource.UpdatedAt,
		resource.TenantID, resource.HospitalID, resource.Status, resource.Department,
		resource.Sensitivity, resource.PayloadJSON, resource.UpdatedAt)
	if err != nil {
		return fmt.Errorf("oraclestore: saving a resource: %w", err)
	}
	return nil
}

// Resource reads one business resource.
//
// department and sensitivity are nullable, and Oracle cannot tell an empty string
// from NULL, so both come back as the empty string rather than as a distinction
// callers would have to handle differently per engine.
func (s *Store) Resource(ctx context.Context, resourceType, resourceID string) (assignmentstore.Resource, bool, error) {
	const query = `
		SELECT tenant_id, hospital_id, status, department, sensitivity, payload, updated_at
		FROM fhir_resource WHERE resource_type = :1 AND resource_id = :2`

	resource := assignmentstore.Resource{ResourceType: resourceType, ResourceID: resourceID}
	var department, sensitivity sql.NullString
	err := s.db.QueryRowContext(ctx, query, resourceType, resourceID).Scan(
		&resource.TenantID, &resource.HospitalID, &resource.Status,
		&department, &sensitivity, &resource.PayloadJSON, &resource.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return assignmentstore.Resource{}, false, nil
	}
	if err != nil {
		return assignmentstore.Resource{}, false, fmt.Errorf("oraclestore: reading a resource: %w", err)
	}
	resource.Department = department.String
	resource.Sensitivity = sensitivity.String
	return resource, true, nil
}

// DeleteResource removes one instance. Deleting a row that is not there is
// not an error.
func (s *Store) DeleteResource(ctx context.Context, resourceType, resourceID string) error {
	const statement = `DELETE FROM fhir_resource WHERE resource_type = :1 AND resource_id = :2`
	if _, err := s.db.ExecContext(ctx, statement, resourceType, resourceID); err != nil {
		return fmt.Errorf("oraclestore: deleting a resource: %w", err)
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
		WHERE resource_type = :1 AND tenant_id = :2 AND hospital_id = :3`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, query.ResourceType, query.TenantID, query.HospitalID).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("oraclestore: counting resources: %w", err)
	}

	const pageQuery = `
		SELECT resource_id, status, department, sensitivity, payload, updated_at
		FROM fhir_resource
		WHERE resource_type = :1 AND tenant_id = :2 AND hospital_id = :3
		ORDER BY resource_id
		OFFSET :4 ROWS FETCH NEXT :5 ROWS ONLY`
	rows, err := s.db.QueryContext(ctx, pageQuery,
		query.ResourceType, query.TenantID, query.HospitalID, query.Offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("oraclestore: listing resources: %w", err)
	}
	defer rows.Close()

	var page []assignmentstore.Resource
	for rows.Next() {
		resource := assignmentstore.Resource{
			ResourceType: query.ResourceType,
			TenantID:     query.TenantID,
			HospitalID:   query.HospitalID,
		}
		var department, sensitivity sql.NullString
		if err := rows.Scan(&resource.ResourceID, &resource.Status,
			&department, &sensitivity, &resource.PayloadJSON, &resource.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("oraclestore: scanning a resource: %w", err)
		}
		resource.Department = department.String
		resource.Sensitivity = sensitivity.String
		page = append(page, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("oraclestore: listing resources: %w", err)
	}
	return page, total, nil
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
