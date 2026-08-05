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

func TestRoleSearchReportsCanonicalIdentifiers(t *testing.T) {
	directory := &recordingDirectory{
		roles: idpdirectory.Page[idpdirectory.RoleRef]{
			Items: []idpdirectory.RoleRef{{
				CanonicalID: "kc:cerbos-poc:patient-app:doctor",
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
	if !strings.Contains(rec.Body.String(), "kc:cerbos-poc:patient-app:doctor") {
		t.Errorf("response carries no canonical identifier: %s", rec.Body)
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

func get(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	return request.WithContext(tokenauth.WithIdentity(request.Context(), tokenauth.Identity{
		PrincipalID: "user-admin",
		TenantID:    "tenant-a",
		HospitalID:  "hospital-1",
		Roles:       []string{"kc:cerbos-poc:patient-app:administrator"},
	}))
}

type recordingDirectory struct {
	calls      int
	tenant     idpdirectory.TenantID
	userSearch idpdirectory.UserSearch
	roleSearch idpdirectory.RoleSearch

	users idpdirectory.Page[idpdirectory.UserRef]
	roles idpdirectory.Page[idpdirectory.RoleRef]
	err   error
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

func (d *recordingDirectory) ResolveRuntimeRoles(_ context.Context, token tokenverifier.VerifiedToken, _ idpdirectory.TenantID) ([]string, error) {
	return token.Roles, nil
}
