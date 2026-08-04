package assignments_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

var decidedAt = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// recordingMatrix is a role matrix that answers from memory and counts what it
// was asked. Counting is the point: the number of round trips a decision costs
// is a property of the resolver, not an implementation detail.
type recordingMatrix struct {
	permissions []assignmentstore.RolePermission
	revision    assignmentstore.PermissionRevision
	hasRevision bool
	err         error

	queries   []assignmentstore.ActiveRolePermissionQuery
	revisions int
}

func (m *recordingMatrix) ActiveRolePermissions(_ context.Context, query assignmentstore.ActiveRolePermissionQuery) ([]assignmentstore.RolePermission, error) {
	m.queries = append(m.queries, query)
	if m.err != nil {
		return nil, m.err
	}
	if len(query.RoleExternalIDs) == 0 {
		return nil, nil
	}

	wanted := make(map[string]bool, len(query.RoleExternalIDs))
	for _, role := range query.RoleExternalIDs {
		wanted[role] = true
	}

	var matching []assignmentstore.RolePermission
	for _, permission := range m.permissions {
		key := permission.Key
		if key.TenantID != query.TenantID || key.ResourceKey != query.ResourceKey {
			continue
		}
		if !wanted[key.RoleExternalID] {
			continue
		}
		matching = append(matching, permission)
	}
	return matching, nil
}

func (m *recordingMatrix) PermissionRevision(_ context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	m.revisions++
	if m.err != nil {
		return assignmentstore.PermissionRevision{}, false, m.err
	}
	if !m.hasRevision || m.revision.TenantID != tenantID {
		return assignmentstore.PermissionRevision{}, false, nil
	}
	return m.revision, true, nil
}

func grant(tenant, role, action string, enabled bool) assignmentstore.RolePermission {
	return assignmentstore.RolePermission{
		Key: assignmentstore.RolePermissionKey{
			TenantID: tenant, RoleExternalID: role,
			ResourceKey: "patient_record", ActionKey: action,
		},
		Enabled: enabled,
	}
}

func newResolver(matrix assignments.RoleMatrix) *assignments.Resolver {
	return assignments.NewResolver(assignments.ResolverConfig{
		Matrix: matrix,
		Now:    func() time.Time { return decidedAt },
	})
}

func doctorQuery(roles ...string) authz.AssignmentQuery {
	return authz.AssignmentQuery{
		TenantID:     "tenant-a",
		HospitalID:   "hospital-1",
		PrincipalID:  "user-doctor",
		ResourceKind: "patient_record",
		ResourceID:   "patient-456",
		IdPRoles:     roles,
	}
}

func TestASeededRoleGrantResolvesToARoleGrantedAction(t *testing.T) {
	matrix := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{
			grant("tenant-a", "kc:realm:patient-app:doctor", "read", true),
		},
	}

	input, err := newResolver(matrix).For(context.Background(),
		doctorQuery("kc:realm:patient-app:doctor"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if !contains(assembled.RoleGrantedActions, "read") {
		t.Errorf("roleGrantedActions = %v, want it to contain read", assembled.RoleGrantedActions)
	}
}

// A disabled row grants nothing, and it is emphatically not a user-level
// denial: putting it in userRevokedActions would let a switched-off role
// permission outrank an explicit user grant.
func TestADisabledRolePermissionGrantsNothingAndRevokesNothing(t *testing.T) {
	matrix := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{
			grant("tenant-a", "kc:realm:patient-app:doctor", "delete", false),
		},
	}

	input, err := newResolver(matrix).For(context.Background(),
		doctorQuery("kc:realm:patient-app:doctor"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if contains(assembled.RoleGrantedActions, "delete") {
		t.Errorf("a disabled row granted delete: %v", assembled.RoleGrantedActions)
	}
	if contains(assembled.UserRevokedActions, "delete") {
		t.Errorf("a disabled row was reported as a user revoke: %v", assembled.UserRevokedActions)
	}
	if contains(assembled.UserGrantedActions, "delete") {
		t.Errorf("a disabled row was reported as a user grant: %v", assembled.UserGrantedActions)
	}
}

// Validity is judged at the instant the decision is taken, and the resolver
// must be the one to say when that was. A resolver that let the database decide
// would make a decision and its replay disagree.
func TestTheValidityWindowIsJudgedAtTheInstantOfTheDecision(t *testing.T) {
	matrix := &recordingMatrix{}

	if _, err := newResolver(matrix).For(context.Background(),
		doctorQuery("kc:realm:patient-app:doctor")); err != nil {
		t.Fatalf("For: %v", err)
	}

	if len(matrix.queries) != 1 {
		t.Fatalf("queries = %d, want 1", len(matrix.queries))
	}
	if !matrix.queries[0].At.Equal(decidedAt) {
		t.Errorf("the matrix was queried at %s, want %s", matrix.queries[0].At, decidedAt)
	}
}

// Tenant isolation is not the database's kindness; the resolver must ask for
// one tenant and one resource and nothing else.
func TestTheMatrixIsQueriedForTheCallersTenantAndResourceOnly(t *testing.T) {
	matrix := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{
			grant("tenant-b", "kc:realm:patient-app:doctor", "read", true),
		},
	}

	input, err := newResolver(matrix).For(context.Background(),
		doctorQuery("kc:realm:patient-app:doctor"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if len(assembled.RoleGrantedActions) != 0 {
		t.Errorf("another tenant's grant reached the decision: %v", assembled.RoleGrantedActions)
	}
	query := matrix.queries[0]
	if query.TenantID != "tenant-a" || query.ResourceKey != "patient_record" {
		t.Errorf("the matrix was queried for %s/%s, want tenant-a/patient_record",
			query.TenantID, query.ResourceKey)
	}
}

// §11.2: a principal holds many roles, and resolving them must cost a bounded
// number of round trips rather than one per role.
func TestManyRolesCostABoundedNumberOfQueries(t *testing.T) {
	roles := make([]string, 0, 70)
	permissions := make([]assignmentstore.RolePermission, 0, 70)
	for index := range 70 {
		role := fmt.Sprintf("kc:realm:patient-app:role-%02d", index)
		roles = append(roles, role)
		permissions = append(permissions, grant("tenant-a", role, fmt.Sprintf("action-%02d", index), true))
	}
	matrix := &recordingMatrix{permissions: permissions}

	input, err := newResolver(matrix).For(context.Background(), doctorQuery(roles...))
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	if len(matrix.queries) != 1 {
		t.Errorf("resolving %d roles took %d matrix queries, want 1", len(roles), len(matrix.queries))
	}
	if matrix.revisions > 1 {
		t.Errorf("resolving one decision took %d revision reads, want at most 1", matrix.revisions)
	}
	if got := len(matrix.queries[0].RoleExternalIDs); got != len(roles) {
		t.Errorf("the single query carried %d roles, want %d", got, len(roles))
	}
	if got := len(permissioncontext.Assemble(input).RoleGrantedActions); got != len(roles) {
		t.Errorf("resolved %d granted actions, want %d", got, len(roles))
	}
}

// §11.3: the decision reports the revision of the matrix it was taken against,
// which is the tenant's current revision and not a constant.
func TestTheResolvedRevisionIsTheTenantsCurrentRevision(t *testing.T) {
	matrix := &recordingMatrix{
		hasRevision: true,
		revision:    assignmentstore.PermissionRevision{TenantID: "tenant-a", Revision: 184},
	}

	input, err := newResolver(matrix).For(context.Background(),
		doctorQuery("kc:realm:patient-app:doctor"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if input.Revision != 184 {
		t.Errorf("revision = %d, want 184", input.Revision)
	}
}

// A tenant whose matrix has never been saved has no revision yet. That is not
// an error, and it must not be reported as some other tenant's revision.
func TestATenantWithNoSavedMatrixResolvesToRevisionZero(t *testing.T) {
	matrix := &recordingMatrix{}

	input, err := newResolver(matrix).For(context.Background(),
		doctorQuery("kc:realm:patient-app:doctor"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if input.Revision != 0 {
		t.Errorf("revision = %d, want 0", input.Revision)
	}
}

// A principal holding no roles must resolve to no permissions without the
// resolver asking the database for the tenant's whole matrix.
func TestAPrincipalWithNoRolesResolvesToNoPermissions(t *testing.T) {
	matrix := &recordingMatrix{
		permissions: []assignmentstore.RolePermission{
			grant("tenant-a", "kc:realm:patient-app:doctor", "read", true),
		},
	}

	input, err := newResolver(matrix).For(context.Background(), doctorQuery())
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if len(assembled.RoleGrantedActions) != 0 {
		t.Errorf("a principal with no roles resolved %v, want nothing", assembled.RoleGrantedActions)
	}
}

// A role claim arrives from a token, so it can carry blanks and repeats. Neither
// should reach the database: a blank is a lookup for the empty role, and a
// repeat is a bind placeholder that buys no extra answer.
func TestABlankOrRepeatedRoleClaimNeverReachesTheQuery(t *testing.T) {
	matrix := &recordingMatrix{}

	query := doctorQuery("kc:realm:patient-app:doctor", "", "kc:realm:patient-app:doctor")
	if _, err := newResolver(matrix).For(context.Background(), query); err != nil {
		t.Fatalf("For: %v", err)
	}

	asked := matrix.queries[0].RoleExternalIDs
	if len(asked) != 1 || asked[0] != "kc:realm:patient-app:doctor" {
		t.Errorf("the matrix was asked for %v, want the one role once", asked)
	}
}

// An unreachable database must not read as "this principal has no permissions".
// That failure mode turns an outage into a silent, total grant of nothing,
// which the caller cannot distinguish from a legitimate default deny.
func TestAnUnreachableMatrixIsAnErrorRatherThanAnEmptyResult(t *testing.T) {
	matrix := &recordingMatrix{err: errors.New("connection refused")}

	if _, err := newResolver(matrix).For(context.Background(),
		doctorQuery("kc:realm:patient-app:doctor")); err == nil {
		t.Fatal("For returned no error when the matrix was unreachable")
	}
}
