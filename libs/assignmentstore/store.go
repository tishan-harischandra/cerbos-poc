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
	"errors"
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

// ActiveRolePermissionQuery asks for the role matrix that is in force for one
// tenant, one set of canonical roles and one resource at one instant.
//
// The role set is plural because a principal holds many roles at once and the
// decision path resolves them together (§11.2). Asking per role would put one
// round trip per role in front of every authorization question.
type ActiveRolePermissionQuery struct {
	TenantID string
	// RoleExternalIDs are canonical role identifiers (§7.5). Empty means the
	// principal holds no roles, which resolves to no permissions rather than
	// to the tenant's whole matrix.
	RoleExternalIDs []string
	ResourceKey     string
	// At is the instant the validity windows are judged against. It is a
	// parameter rather than the database clock so a decision, a test and a
	// replay all agree on when "now" was.
	At time.Time
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
	// Reason is the mandatory administrative justification for a GRANT or
	// REVOKE (§9.3). Empty for rows SaveUserOverride writes directly - the
	// bulk-seeding path (issue #24) has no administrator behind it - but
	// SaveUserOverrideWrite refuses to write a GRANT or REVOKE without one.
	Reason string
}

// ActiveUserOverridesQuery asks for the user overrides in force for one
// tenant, hospital, user and resource at one instant.
//
// Both a tenant/hospital-wide override and one scoped to a named resource
// instance can exist for the same action (§6.2's "above key + resource
// instance ID or selector"), so a query for one resource instance reads both:
// the wide row applies to every instance, and the instance-scoped row narrows
// further. Which of them the decision should honour is a §6.3 precedence
// question for Cerbos policy, not a question this port answers.
type ActiveUserOverridesQuery struct {
	TenantID       string
	HospitalID     string
	UserExternalID string
	ResourceKey    string
	// ResourceInstanceID is the instance a decision is being taken about.
	// Empty means only the tenant/hospital-wide overrides are wanted; a
	// non-empty value also includes any row scoped to that exact instance.
	ResourceInstanceID string
	// At is the instant the validity windows are judged against.
	At time.Time
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

// RolePermissionInput is one row of a role-matrix slice a caller wants
// written (§9.4). It carries no revision of its own: SaveRoleMatrix stamps
// every row it writes with the transaction's new tenant-wide revision
// (§10.1), because the point of the expected-revision check is that the whole
// slice moves forward together.
type RolePermissionInput struct {
	ResourceKey string
	ActionKey   string
	Enabled     bool
	ValidFrom   time.Time
	ValidUntil  time.Time
}

// RoleMatrixWrite is the input to SaveRoleMatrix: one role's whole permission
// slice within one tenant, plus the audit event and outbox event that must
// commit with it (§9.4, §10.1, §16.1).
type RoleMatrixWrite struct {
	TenantID       string
	RoleExternalID string
	// ExpectedRevision is the tenant permission revision the caller last
	// read (§9.4's "Replace role permissions using expected revision").
	// Zero means the caller believes the tenant has never had a matrix
	// saved.
	ExpectedRevision int64
	Permissions      []RolePermissionInput
	// Audit.TenantID is ignored; the write always audits against
	// RoleMatrixWrite.TenantID so the two can never disagree.
	Audit  AuditEvent
	Outbox OutboxEvent
}

// ErrRevisionConflict is returned by SaveRoleMatrix, with nothing written,
// when ExpectedRevision no longer matches the tenant's stored permission
// revision (§10.1).
var ErrRevisionConflict = errors.New("assignmentstore: expected revision is stale")

// UserOverrideWrite is the input to SaveUserOverrideWrite: one tri-state
// change to a single user override, plus the audit event and outbox event
// that must commit with it (§9.3, §9.4, §10.1).
type UserOverrideWrite struct {
	Key UserOverrideKey
	// Effect is EffectGrant or EffectRevoke to persist a GRANT or REVOKE.
	// The zero value means INHERIT: SaveUserOverrideWrite clears any
	// existing row for Key rather than upserting one, because INHERIT is
	// the absence of a row (§8.3), not a storable effect.
	Effect     OverrideEffect
	Reason     string
	ValidFrom  time.Time
	ValidUntil time.Time
	// ExpectedRevision is the tenant permission revision the caller last
	// read, the same expected-revision guard SaveRoleMatrix uses (§9.4).
	ExpectedRevision int64
	// Audit.TenantID and Audit.HospitalID are ignored; the write always
	// audits against Key.TenantID and Key.HospitalID so the two can never
	// disagree.
	Audit  AuditEvent
	Outbox OutboxEvent
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

// ListResourcesQuery pages through instances of one resource type. It always
// scopes to a tenant and a hospital: the resource service (issue #9) is a
// PEP, not a public index, and a list that could cross a tenant boundary
// would be the one read path §21's isolation invariant does not cover.
type ListResourcesQuery struct {
	ResourceType string
	TenantID     string
	HospitalID   string
	// Limit bounds the page size. Zero means DefaultListLimit.
	Limit int
	// Offset is the number of matching rows to skip, for the next page.
	Offset int
}

// DefaultListLimit is the page size ListResources uses when a query's Limit
// is zero, so a caller that forgets to set one gets a bounded page rather
// than every instance of a resource type in the tenant.
const DefaultListLimit = 50

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

	// ActiveRolePermissions reads, in one round trip, every role permission
	// in force for the queried roles. Rows outside their validity window are
	// left out; disabled rows are returned marked disabled, because "grants
	// nothing" and "denies" are different facts (§8.3).
	ActiveRolePermissions(ctx context.Context, query ActiveRolePermissionQuery) ([]RolePermission, error)

	// RolePermissionsForRole reads every permission row a role carries
	// across every resource, regardless of validity window or enabled
	// state (§9.2's role matrix screen: an administrator editing a role
	// needs to see a disabled or expired grant exactly as stored, not
	// filtered the way a live decision would filter it).
	RolePermissionsForRole(ctx context.Context, tenantID, roleExternalID string) ([]RolePermission, error)

	SaveUserOverride(ctx context.Context, override UserOverride) error
	UserOverride(ctx context.Context, key UserOverrideKey) (UserOverride, bool, error)

	// ActiveUserOverrides reads, in one round trip, every override in force
	// for one principal and one resource. Rows outside their validity window
	// are left out; disabled rows are returned marked disabled, for the same
	// reason ActiveRolePermissions returns disabled role rows (§8.3).
	ActiveUserOverrides(ctx context.Context, query ActiveUserOverridesQuery) ([]UserOverride, error)

	SavePermissionRevision(ctx context.Context, revision PermissionRevision) error
	PermissionRevision(ctx context.Context, tenantID string) (PermissionRevision, bool, error)

	AppendAuditEvent(ctx context.Context, event AuditEvent) error
	AuditEvent(ctx context.Context, eventID string) (AuditEvent, bool, error)

	AppendOutboxEvent(ctx context.Context, event OutboxEvent) error
	OutboxEvent(ctx context.Context, eventID string) (OutboxEvent, bool, error)

	// UnpublishedOutboxEvents reads up to limit outbox rows that have not
	// yet been published, oldest first, so a publisher drains them in the
	// order they were written.
	UnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error)
	// MarkOutboxEventPublished records that an outbox row was published.
	// Marking an already-published row again is not an error: at-least-once
	// delivery means a publisher may see the same row twice.
	MarkOutboxEventPublished(ctx context.Context, eventID string, publishedAt time.Time) error

	// SaveRoleMatrix atomically writes one role's permission slice: every
	// row's upsert, the audit event, the outbox event and the tenant's
	// permission-revision bump commit as a single unit or none do (§9.4,
	// §10.1, §16.1). It returns ErrRevisionConflict, with nothing written,
	// if write.ExpectedRevision no longer matches the tenant's stored
	// revision.
	SaveRoleMatrix(ctx context.Context, write RoleMatrixWrite) (newRevision int64, err error)

	// SaveUserOverrideWrite atomically applies one tri-state change to a
	// user override: the row's upsert (GRANT/REVOKE) or delete (INHERIT),
	// the audit event, the outbox event and the tenant's permission-revision
	// bump commit as a single unit or none do (§9.3, §9.4, §10.1, §16.1). It
	// returns ErrRevisionConflict, with nothing written, if
	// write.ExpectedRevision no longer matches the tenant's stored revision.
	SaveUserOverrideWrite(ctx context.Context, write UserOverrideWrite) (newRevision int64, err error)

	SaveCapability(ctx context.Context, capability Capability) error
	Capability(ctx context.Context, capabilityKey string) (Capability, bool, error)

	SaveInstallationConfig(ctx context.Context, config InstallationConfig) error
	InstallationConfig(ctx context.Context, installationID string) (InstallationConfig, bool, error)

	SaveResource(ctx context.Context, resource Resource) error
	Resource(ctx context.Context, resourceType, resourceID string) (Resource, bool, error)
	// DeleteResource removes one instance. Deleting an instance that does not
	// exist is not an error: it leaves the store in the state the caller
	// asked for.
	DeleteResource(ctx context.Context, resourceType, resourceID string) error
	// ListResources pages through instances of one resource type within one
	// tenant and hospital, ordered by resource_id so that pagination is
	// stable across calls. TotalCount is the count of every matching row,
	// not just this page, so a caller can tell whether more pages remain.
	ListResources(ctx context.Context, query ListResourcesQuery) (page []Resource, totalCount int, err error)

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
