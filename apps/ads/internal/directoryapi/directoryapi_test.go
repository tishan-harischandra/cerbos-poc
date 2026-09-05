package directoryapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/directoryapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

const adminSecret = "a-secret-nobody-should-see"

func TestUserSearchReturnsOnePageAndSaysWhetherThereIsAnother(t *testing.T) {
	directory := &recordingDirectory{
		users: idpdirectory.Page[idpdirectory.UserRef]{
			Items: []idpdirectory.UserRef{
				{ExternalID: "user-doctor", Username: "doctor", DisplayName: "Dana Doctor"},
				{ExternalID: "user-auditor", Username: "auditor", DisplayName: "Alex Auditor"},
			},
			Offset: 20, Limit: 2, HasMore: true,
		},
	}
	handler := directoryapi.NewUsersHandler(directoryapi.Config{Directory: directory})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/directory/users?query=do&offset=20&limit=2"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var body struct {
		Items []struct {
			ExternalID  string `json:"externalId"`
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
		} `json:"items"`
		Offset  int  `json:"offset"`
		Limit   int  `json:"limit"`
		HasMore bool `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}

	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	if body.Items[0].ExternalID != "user-doctor" {
		t.Errorf("first item = %q, want the directory's first user", body.Items[0].ExternalID)
	}
	if body.Offset != 20 || body.Limit != 2 || !body.HasMore {
		t.Errorf("page = offset %d limit %d hasMore %t, want 20/2/true",
			body.Offset, body.Limit, body.HasMore)
	}
	if directory.userSearch.Page.Offset != 20 || directory.userSearch.Page.Limit != 2 {
		t.Errorf("the directory was asked for %+v, want the caller's window", directory.userSearch.Page)
	}
	if directory.userSearch.Query != "do" {
		t.Errorf("query = %q, want the caller's query", directory.userSearch.Query)
	}
}

// The tenant is the token's, never the query string's: otherwise any
// authenticated user could enumerate another tenant's directory.
func TestTheTenantSearchedIsTheOneInTheToken(t *testing.T) {
	directory := &recordingDirectory{}
	handler := directoryapi.NewUsersHandler(directoryapi.Config{Directory: directory})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/directory/users?tenantId=tenant-b"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: a tenant in the query string is refused", rec.Code, http.StatusBadRequest)
	}
	if directory.calls != 0 {
		t.Error("the directory was searched for a request that named its own tenant")
	}
}

// A deployment serving more than one realm (issue #77) routes each request
// to its own tenant's directory client, never a different tenant's.
func TestASecondTenantIsRoutedToItsOwnDirectory(t *testing.T) {
	tenantA := &recordingDirectory{}
	tenantB := &recordingDirectory{
		users: idpdirectory.Page[idpdirectory.UserRef]{
			Items: []idpdirectory.UserRef{{ExternalID: "user-b", Username: "b-user"}},
		},
	}
	handler := directoryapi.NewUsersHandler(directoryapi.Config{
		Directories: map[string]idpdirectory.IdentityDirectory{
			"tenant-a": tenantA,
			"tenant-b": tenantB,
		},
	})

	request := httptest.NewRequest(http.MethodGet, "/internal/directory/users", nil)
	request = request.WithContext(tokenauth.WithIdentity(request.Context(), tokenauth.Identity{
		PrincipalID: "user-admin",
		TenantID:    "tenant-b",
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if tenantA.calls != 0 {
		t.Error("tenant a's directory was searched for a tenant b request")
	}
	if tenantB.calls != 1 {
		t.Errorf("tenant b's directory was searched %d times, want 1", tenantB.calls)
	}
}

// A verified tenant with no configured directory is a server-side gap, not
// a caller mistake: it fails the same way an unreachable directory would,
// rather than leaking which tenants exist.
func TestATenantWithNoConfiguredDirectoryFailsCleanly(t *testing.T) {
	handler := directoryapi.NewUsersHandler(directoryapi.Config{
		Directories: map[string]idpdirectory.IdentityDirectory{
			"tenant-a": &recordingDirectory{},
		},
	})

	request := httptest.NewRequest(http.MethodGet, "/internal/directory/users", nil)
	request = request.WithContext(tokenauth.WithIdentity(request.Context(), tokenauth.Identity{
		PrincipalID: "user-admin",
		TenantID:    "tenant-c",
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestTheTenantComesFromTheVerifiedIdentity(t *testing.T) {
	directory := &recordingDirectory{}
	handler := directoryapi.NewUsersHandler(directoryapi.Config{Directory: directory})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/directory/users"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if directory.tenant != "tenant-a" {
		t.Errorf("tenant searched = %q, want the token's tenant", directory.tenant)
	}
}

// issue #86: a tenant onboarded at runtime is reachable through a lookup
// backed by something more dynamic than a plain map - idpdirectory.Registry
// in production - without disturbing any caller still using Directories.
func TestDirectoriesLookupTakesPriorityOverDirectories(t *testing.T) {
	staleMap := &recordingDirectory{}
	dynamic := &recordingDirectory{
		users: idpdirectory.Page[idpdirectory.UserRef]{Items: []idpdirectory.UserRef{{ExternalID: "user-onboarded"}}},
	}
	handler := directoryapi.NewUsersHandler(directoryapi.Config{
		Directories: map[string]idpdirectory.IdentityDirectory{"tenant-a": staleMap},
		DirectoriesLookup: func(tenantID string) (idpdirectory.IdentityDirectory, bool) {
			return dynamic, tenantID == "tenant-a"
		},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/directory/users"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if staleMap.calls != 0 {
		t.Error("the stale Directories map was consulted even though DirectoriesLookup was set")
	}
	if dynamic.calls != 1 {
		t.Error("DirectoriesLookup's own directory was never consulted")
	}
}

func TestRoleSearchReportsCanonicalIdentifiers(t *testing.T) {
	directory := &recordingDirectory{
		roles: idpdirectory.Page[idpdirectory.RoleRef]{
			Items: []idpdirectory.RoleRef{{
				CanonicalID: "kc:tenant-a:realm:doctor",
				ExternalID:  "58d1e7c8-role",
				Name:        "doctor",
			}},
			Limit: 50,
		},
	}
	handler := directoryapi.NewRolesHandler(directoryapi.Config{Directory: directory})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/directory/roles"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	// The canonical identifier is what the role-permission matrix is keyed by,
	// so a console that could not read it could not write a matrix row.
	if !strings.Contains(rec.Body.String(), "kc:tenant-a:realm:doctor") {
		t.Errorf("response carries no canonical identifier: %s", rec.Body)
	}
}

func TestOrganizationsReturnsOnePageAndSaysWhetherThereIsAnother(t *testing.T) {
	directory := &recordingDirectory{
		organizations: idpdirectory.Page[idpdirectory.OrganizationRef]{
			Items: []idpdirectory.OrganizationRef{
				{ExternalID: "org-north", Alias: "north-hospital", Name: "North Hospital"},
			},
			Offset: 0, Limit: 50, HasMore: true,
		},
	}
	handler := directoryapi.NewOrganizationsHandler(directoryapi.Config{Directory: directory})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/directory/organizations"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "north-hospital") {
		t.Errorf("response carries no organization alias: %s", rec.Body)
	}
	if directory.tenant != "tenant-a" {
		t.Errorf("tenant = %q, want the caller's own tenant", directory.tenant)
	}
}

func TestOrganizationMembersReportsOneWindowOfMembers(t *testing.T) {
	directory := &recordingDirectory{
		organizationMembers: idpdirectory.Page[idpdirectory.UserRef]{
			Items: []idpdirectory.UserRef{{ExternalID: "user-doctor", Username: "doctor"}},
			Limit: 50,
		},
	}
	handler := directoryapi.NewOrganizationMembersHandler(directoryapi.Config{Directory: directory})

	req := get("/internal/directory/organizations/org-north/members")
	req.SetPathValue("externalId", "org-north")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if directory.organizationMembersFor != "org-north" {
		t.Errorf("organization queried = %q, want org-north", directory.organizationMembersFor)
	}
	if !strings.Contains(rec.Body.String(), "user-doctor") {
		t.Errorf("response carries no member: %s", rec.Body)
	}
}

func TestUserOrganizationsReportsTheOrganizationsThatUserBelongsTo(t *testing.T) {
	directory := &recordingDirectory{
		userOrganizations: []idpdirectory.OrganizationRef{
			{ExternalID: "org-north", Alias: "north-hospital"},
			{ExternalID: "org-south", Alias: "south-hospital"},
		},
	}
	handler := directoryapi.NewUserOrganizationsHandler(directoryapi.Config{Directory: directory})

	req := get("/internal/directory/users/user-doctor-multi/organizations")
	req.SetPathValue("externalId", "user-doctor-multi")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if directory.userOrganizationsFor != "user-doctor-multi" {
		t.Errorf("user queried = %q, want user-doctor-multi", directory.userOrganizationsFor)
	}
	if !strings.Contains(rec.Body.String(), "north-hospital") || !strings.Contains(rec.Body.String(), "south-hospital") {
		t.Errorf("response is missing a membership: %s", rec.Body)
	}
}

func TestUserOrganizationsRequiresAuthentication(t *testing.T) {
	directory := &recordingDirectory{}
	handler := directoryapi.NewUserOrganizationsHandler(directoryapi.Config{Directory: directory})

	req := httptest.NewRequest(http.MethodGet, "/internal/directory/users/user-doctor/organizations", nil)
	req.SetPathValue("externalId", "user-doctor")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if directory.calls != 0 {
		t.Error("the directory was queried for an unauthenticated request")
	}
}

// §16.1 and §7.4: the IdP machine credential must never reach a browser. The
// realistic leak is an error path, so the failing case is the one asserted.
func TestADirectoryFailureNeverEchoesTheAdminCredential(t *testing.T) {
	directory := &recordingDirectory{
		err: fmt.Errorf("keycloak rejected the request for client_secret=%s", adminSecret),
	}
	handler := directoryapi.NewUsersHandler(directoryapi.Config{Directory: directory})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/directory/users"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(rec.Body.String(), adminSecret) {
		t.Errorf("the response echoed the admin credential: %s", rec.Body)
	}
}

func TestAnUnauthenticatedRequestIsRefused(t *testing.T) {
	directory := &recordingDirectory{}
	handler := directoryapi.NewUsersHandler(directoryapi.Config{Directory: directory})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/directory/users", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if directory.calls != 0 {
		t.Error("the directory was searched for an unauthenticated request")
	}
}

func TestAnUnreadablePageWindowIsRefusedRatherThanGuessed(t *testing.T) {
	for _, query := range []string{"?limit=all", "?offset=-1", "?limit=0"} {
		t.Run(query, func(t *testing.T) {
			directory := &recordingDirectory{}
			handler := directoryapi.NewUsersHandler(directoryapi.Config{Directory: directory})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, get("/internal/directory/users"+query))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestUserRolesReportsTheRolesDirectlyAssignedToThatUser(t *testing.T) {
	directory := &recordingDirectory{
		userRoles: []idpdirectory.RoleRef{
			{CanonicalID: "kc:tenant-a:realm:doctor", ExternalID: "58d1e7c8-role", Name: "doctor"},
		},
	}
	handler := directoryapi.NewUserRolesHandler(directoryapi.Config{Directory: directory})

	req := get("/internal/directory/users/user-doctor/roles")
	req.SetPathValue("externalId", "user-doctor")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "kc:tenant-a:realm:doctor") {
		t.Errorf("response carries no canonical identifier: %s", rec.Body)
	}
	if directory.userRolesFor != "user-doctor" {
		t.Errorf("the directory was asked about %q, want user-doctor", directory.userRolesFor)
	}
}

func TestUserRolesRequiresAuthentication(t *testing.T) {
	directory := &recordingDirectory{}
	handler := directoryapi.NewUserRolesHandler(directoryapi.Config{Directory: directory})

	req := httptest.NewRequest(http.MethodGet, "/internal/directory/users/user-doctor/roles", nil)
	req.SetPathValue("externalId", "user-doctor")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if directory.calls != 0 {
		t.Error("the directory was called for an unauthenticated request")
	}
}

func get(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	return request.WithContext(tokenauth.WithIdentity(request.Context(), tokenauth.Identity{
		PrincipalID: "user-admin",
		TenantID:    "tenant-a",
		HospitalID:  "hospital-1",
		Roles:       []string{"kc:tenant-a:realm:administrator"},
	}))
}

type recordingDirectory struct {
	calls                   int
	tenant                  idpdirectory.TenantID
	userSearch              idpdirectory.UserSearch
	roleSearch              idpdirectory.RoleSearch
	userRolesFor            string
	organizationSearch      idpdirectory.OrganizationSearch
	userOrganizationsFor    string
	organizationMembersFor  string
	organizationMembersPage idpdirectory.PageRequest

	users               idpdirectory.Page[idpdirectory.UserRef]
	roles               idpdirectory.Page[idpdirectory.RoleRef]
	userRoles           []idpdirectory.RoleRef
	organizations       idpdirectory.Page[idpdirectory.OrganizationRef]
	userOrganizations   []idpdirectory.OrganizationRef
	organizationMembers idpdirectory.Page[idpdirectory.UserRef]
	err                 error
}

func (d *recordingDirectory) SearchUsers(_ context.Context, tenant idpdirectory.TenantID, query idpdirectory.UserSearch) (idpdirectory.Page[idpdirectory.UserRef], error) {
	d.calls++
	d.tenant, d.userSearch = tenant, query
	return d.users, d.err
}

func (d *recordingDirectory) SearchRoles(_ context.Context, tenant idpdirectory.TenantID, query idpdirectory.RoleSearch) (idpdirectory.Page[idpdirectory.RoleRef], error) {
	d.calls++
	d.tenant, d.roleSearch = tenant, query
	return d.roles, d.err
}

func (d *recordingDirectory) GetUser(context.Context, idpdirectory.TenantID, string) (idpdirectory.UserRef, error) {
	return idpdirectory.UserRef{}, idpdirectory.ErrNotFound
}

func (d *recordingDirectory) GetRole(context.Context, idpdirectory.TenantID, string) (idpdirectory.RoleRef, error) {
	return idpdirectory.RoleRef{}, idpdirectory.ErrNotFound
}

func (d *recordingDirectory) GetUserRoles(_ context.Context, tenant idpdirectory.TenantID, userExternalID string) ([]idpdirectory.RoleRef, error) {
	d.calls++
	d.tenant, d.userRolesFor = tenant, userExternalID
	return d.userRoles, d.err
}

func (d *recordingDirectory) OrganizationsOfTenant(_ context.Context, tenant idpdirectory.TenantID, query idpdirectory.OrganizationSearch) (idpdirectory.Page[idpdirectory.OrganizationRef], error) {
	d.calls++
	d.tenant, d.organizationSearch = tenant, query
	return d.organizations, d.err
}

func (d *recordingDirectory) OrganizationsOfUser(_ context.Context, tenant idpdirectory.TenantID, userExternalID string) ([]idpdirectory.OrganizationRef, error) {
	d.calls++
	d.tenant, d.userOrganizationsFor = tenant, userExternalID
	return d.userOrganizations, d.err
}

func (d *recordingDirectory) MembersOfOrganization(_ context.Context, tenant idpdirectory.TenantID, organizationExternalID string, page idpdirectory.PageRequest) (idpdirectory.Page[idpdirectory.UserRef], error) {
	d.calls++
	d.tenant, d.organizationMembersFor, d.organizationMembersPage = tenant, organizationExternalID, page
	return d.organizationMembers, d.err
}

func (d *recordingDirectory) ResolveRuntimeRoles(_ context.Context, token tokenverifier.VerifiedToken, _ idpdirectory.TenantID) ([]string, error) {
	return token.Roles, nil
}
