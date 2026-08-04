// Package assignmentstore is the port through which services reach the
// authorization database (§8.1).
//
// It exists so that no dialect-specific SQL leaks into service logic: the
// interface below is the only vocabulary callers use, and each supported engine
// supplies its own adapter. PostgreSQL is the default engine and Oracle is a
// first-class target, so anything expressible in only one of them is a defect in
// this package rather than a fact callers have to work around.
package assignmentstore

import (
	"context"
	"time"
)

// NoResourceInstance is the sentinel stored when a user override applies to
// every instance of a resource rather than to one named instance.
//
// The design makes resource_instance_id nullable and puts it in the unique
// override key (§8.2). That combination does not enforce what it promises:
// NULL never equals NULL in a unique index, on either engine, so two
// tenant-wide overrides for the same user, resource and action would both be
// accepted. Oracle compounds it by storing the empty string as NULL, so ""
// cannot be used as a distinct value either. Storing an explicit sentinel is
// what makes the key enforceable and makes both engines behave alike.
const NoResourceInstance = "*"

// OverrideEffect is the effect of a user-level override (§8.3).
type OverrideEffect string

// The two effects a user override can carry. INHERIT is the absence of a row,
// or a row that is disabled, so it is not an effect.
const (
	EffectGrant  OverrideEffect = "GRANT"
	EffectRevoke OverrideEffect = "REVOKE"
)

// RolePermissionKey identifies one role permission (§8.2 unique key).
type RolePermissionKey struct {
	TenantID       string
	RoleExternalID string
	ResourceKey    string
	ActionKey      string
}

// RolePermission is one action a canonical role either grants or does not.
// A disabled row contributes no grant; it is not an explicit deny (§8.3).
type RolePermission struct {
	Key        RolePermissionKey
	Enabled    bool
	ValidFrom  time.Time
	ValidUntil time.Time
	Revision   int64
}

// UserOverrideKey identifies one user override (§8.2 unique key).
type UserOverrideKey struct {
	TenantID       string
	HospitalID     string
	UserExternalID string
	ResourceKey    string
	ActionKey      string
	// ResourceInstanceID is NoResourceInstance when the override applies to
	// every instance of the resource.
	ResourceInstanceID string
}

// UserOverride is one user-level decision about a single action (§8.3).
type UserOverride struct {
	Key        UserOverrideKey
	Effect     OverrideEffect
	Enabled    bool
	ValidFrom  time.Time
	ValidUntil time.Time
	Revision   int64
}

// PermissionRevision is a tenant's current permission revision (§8.1).
//
// One row per tenant, advanced in place by every matrix save. The ADS reads it
// so a decision can say which state of the matrix it was taken against (§11.3).
type PermissionRevision struct {
	TenantID  string
	Revision  int64
	ChangedAt time.Time
}

// AuditEvent is one append-only record of an authorization change (§8.1).
type AuditEvent struct {
	EventID       string
	ActorID       string
	Operation     string
	TargetType    string
	BeforeJSON    string
	AfterJSON     string
	TenantID      string
	HospitalID    string
	CorrelationID string
	CreatedAt     time.Time
}

// OutboxEvent is one row of the transactional outbox (§8.1).
type OutboxEvent struct {
	EventID      string
	AggregateKey string
	EventType    string
	Payload      string
	CreatedAt    time.Time
	PublishedAt  *time.Time
}

// Capability is one composite UI capability definition (§8.1).
type Capability struct {
	CapabilityKey   string
	ModuleKey       string
	ContextType     string
	ExpressionJSON  string
	CatalogRevision int64
	Enabled         bool
}

// InstallationConfig is the per-installation identity provider selection (§8.1).
type InstallationConfig struct {
	InstallationID string
	IDPType        string
	IDPConfigJSON  string
	ActiveRootTag  string
}

// Resource is one business resource the mandatory rules evaluate against.
//
// One polymorphic table rather than a table per FHIR type: the catalog runs to
// ~157 types, and the attributes the policies actually read are the same few in
// every one of them. Those attributes are columns rather than fields inside the
// payload so that no policy condition ever depends on an engine's JSON operators.
type Resource struct {
	ResourceType string
	ResourceID   string
	TenantID     string
	HospitalID   string
	Status       string
	Department   string
	Sensitivity  string
	PayloadJSON  string
	UpdatedAt    time.Time
}

// Store is the port every engine adapter implements.
//
// Every method is expressed in domain terms. Nothing here exposes a dialect, a
// driver type or a SQL fragment, which is what keeps the contract suite in
// TestContract free of dialect-conditional assertions.
type Store interface {
	// Ping reports whether the database is reachable.
	Ping(ctx context.Context) error

	// Close releases the connection pool.
	Close() error

	// Schema reports what the migrations actually created, so portability is
	// asserted against the live database rather than against the changelog.
	Schema() SchemaInspector

	SaveRolePermission(ctx context.Context, permission RolePermission) error
	RolePermission(ctx context.Context, key RolePermissionKey) (RolePermission, bool, error)

	SaveUserOverride(ctx context.Context, override UserOverride) error
	UserOverride(ctx context.Context, key UserOverrideKey) (UserOverride, bool, error)

	SavePermissionRevision(ctx context.Context, revision PermissionRevision) error
	PermissionRevision(ctx context.Context, tenantID string) (PermissionRevision, bool, error)

	AppendAuditEvent(ctx context.Context, event AuditEvent) error
	AuditEvent(ctx context.Context, eventID string) (AuditEvent, bool, error)

	AppendOutboxEvent(ctx context.Context, event OutboxEvent) error
	OutboxEvent(ctx context.Context, eventID string) (OutboxEvent, bool, error)

	SaveCapability(ctx context.Context, capability Capability) error
	Capability(ctx context.Context, capabilityKey string) (Capability, bool, error)

	SaveInstallationConfig(ctx context.Context, config InstallationConfig) error
	InstallationConfig(ctx context.Context, installationID string) (InstallationConfig, bool, error)

	SaveResource(ctx context.Context, resource Resource) error
	Resource(ctx context.Context, resourceType, resourceID string) (Resource, bool, error)

	// Truncate empties the named tables so each contract test starts clean.
	Truncate(ctx context.Context, tables ...string) error
}

// SchemaInspector reports the shape of the migrated schema.
//
// Reading catalog metadata is unavoidably dialect-specific, so it lives behind
// this port too. That is what lets one suite assert the same §8.2 constraints on
// both engines without branching on the engine.
type SchemaInspector interface {
	// Tables lists the authorization tables present, lower-cased.
	Tables(ctx context.Context) ([]string, error)
	// UniqueKeys maps constraint name to its ordered column list, lower-cased.
	UniqueKeys(ctx context.Context, table string) (map[string][]string, error)
	// Indexes maps index name to its ordered column list, lower-cased.
	Indexes(ctx context.Context, table string) (map[string][]string, error)
}
