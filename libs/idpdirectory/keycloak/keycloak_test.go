package keycloak_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/keycloak"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

const (
	realm    = "cerbos-poc"
	clientID = "patient-app"
	tenant   = idpdirectory.TenantID("tenant-a")
)

// The Admin Console pages through a directory with hundreds of thousands of
// users. An adapter that ignored the window would pull the whole realm into
// memory, so the window is asserted on the wire, not only in the result.
func TestUserSearchAsksKeycloakForTheRequestedWindowAndReportsMore(t *testing.T) {
	fake := newFakeKeycloak(t)
	fake.users = make([]user, 0, 120)
	for index := range 120 {
		fake.users = append(fake.users, user{
			ID:        fmt.Sprintf("user-%03d", index),
			Username:  fmt.Sprintf("doctor-%03d", index),
			FirstName: "Demo",
			LastName:  fmt.Sprintf("Doctor %03d", index),
			Email:     fmt.Sprintf("doctor-%03d@example.test", index),
			Enabled:   true,
		})
	}
	defer fake.Close()

	page, err := fake.directory(t).SearchUsers(context.Background(), tenant, idpdirectory.UserSearch{
		Query: "doctor",
		Page:  idpdirectory.PageRequest{Offset: 20, Limit: 10},
	})
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}

	if len(page.Items) != 10 {
		t.Fatalf("returned %d users, want the 10 that were asked for", len(page.Items))
	}
	if page.Items[0].ExternalID != "user-020" {
		t.Errorf("first user = %q, want the one at the requested offset", page.Items[0].ExternalID)
	}
	if !page.HasMore {
		t.Error("HasMore = false, want true: 90 users remain after this window")
	}
	if got := fake.lastQuery("/admin/realms/cerbos-poc/users"); got.Get("first") != "20" || got.Get("max") != "11" {
		// max is one more than the window: the extra row is how HasMore is
		// answered without a second count query on the hot path.
		t.Errorf("user query = %v, want first=20 and max=11", got)
	}
	if got := fake.lastQuery("/admin/realms/cerbos-poc/users").Get("search"); got != "doctor" {
		t.Errorf("search = %q, want the caller's query", got)
	}
}

func TestTheLastPageReportsNoMore(t *testing.T) {
	fake := newFakeKeycloak(t)
	fake.users = []user{
		{ID: "user-001", Username: "doctor", Enabled: true},
		{ID: "user-002", Username: "auditor", Enabled: true},
	}
	defer fake.Close()

	page, err := fake.directory(t).SearchUsers(context.Background(), tenant, idpdirectory.UserSearch{
		Page: idpdirectory.PageRequest{Offset: 0, Limit: 10},
	})
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("returned %d users, want 2", len(page.Items))
	}
	if page.HasMore {
		t.Error("HasMore = true on the last page")
	}
}

// §7.5 requires the directory and token normalisation to agree byte for byte.
// This asserts it against the verifier itself rather than against a literal, so
// the two can only pass together.
func TestRoleSearchProducesTheSameCanonicalIdentifiersAsTokenNormalisation(t *testing.T) {
	fake := newFakeKeycloak(t)
	fake.clientRoles = []role{
		{ID: "58d1e7c8-role", Name: "doctor", Description: "Treating clinician"},
		{ID: "9a2b4f11-role", Name: "auditor", Description: "Read-only auditor"},
	}
	defer fake.Close()

	page, err := fake.directory(t).SearchRoles(context.Background(), tenant, idpdirectory.RoleSearch{})
	if err != nil {
		t.Fatalf("SearchRoles: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("returned %d roles, want 2", len(page.Items))
	}

	fromDirectory := page.Items[0].CanonicalID
	fromToken := canonicalRolesFromToken(t, "doctor")
	if len(fromToken) != 1 || fromDirectory != fromToken[0] {
		t.Errorf("the directory says %q and token normalisation says %v; §7.5 requires one identifier",
			fromDirectory, fromToken)
	}
	if page.Items[0].ExternalID != "58d1e7c8-role" {
		t.Errorf("ExternalID = %q, want Keycloak's stable role id", page.Items[0].ExternalID)
	}
}

func TestGettingAUserThatDoesNotExistReportsNotFound(t *testing.T) {
	fake := newFakeKeycloak(t)
	defer fake.Close()

	_, err := fake.directory(t).GetUser(context.Background(), tenant, "nobody")
	if !errors.Is(err, idpdirectory.ErrNotFound) {
		t.Errorf("GetUser error = %v, want %v", err, idpdirectory.ErrNotFound)
	}
}

func TestGettingAUserReturnsItsStableIdentifierAndDisplayMetadata(t *testing.T) {
	fake := newFakeKeycloak(t)
	fake.users = []user{{
		ID: "user-doctor", Username: "doctor", FirstName: "Dana", LastName: "Doctor",
		Email: "dana@example.test", Enabled: true,
	}}
	defer fake.Close()

	found, err := fake.directory(t).GetUser(context.Background(), tenant, "user-doctor")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if found.ExternalID != "user-doctor" || found.Username != "doctor" {
		t.Errorf("user = %+v, want the stable id and username", found)
	}
	if found.DisplayName != "Dana Doctor" {
		t.Errorf("DisplayName = %q, want %q", found.DisplayName, "Dana Doctor")
	}
}

// The adapter serves exactly the tenant it was configured for. Answering for
// another tenant would mean one realm's roles could be written into another
// tenant's matrix.
func TestAQueryForAnotherTenantIsRefused(t *testing.T) {
	fake := newFakeKeycloak(t)
	defer fake.Close()

	_, err := fake.directory(t).SearchUsers(context.Background(), "tenant-b", idpdirectory.UserSearch{})
	if !errors.Is(err, idpdirectory.ErrUnknownTenant) {
		t.Errorf("SearchUsers error = %v, want %v", err, idpdirectory.ErrUnknownTenant)
	}
}

// §7.3: a dedicated confidential service account, never the browser's token.
func TestTheAdapterAuthenticatesWithItsServiceAccountAndReusesTheToken(t *testing.T) {
	fake := newFakeKeycloak(t)
	fake.users = []user{{ID: "user-001", Username: "doctor", Enabled: true}}
	defer fake.Close()

	directory := fake.directory(t)
	for range 3 {
		if _, err := directory.SearchUsers(context.Background(), tenant, idpdirectory.UserSearch{}); err != nil {
			t.Fatalf("SearchUsers: %v", err)
		}
	}

	if got := fake.tokenRequests.Load(); got != 1 {
		t.Errorf("the service account authenticated %d times, want 1 for three calls", got)
	}
	if got := fake.lastAuthorization.Load(); got == nil || *got != "Bearer service-account-token" {
		t.Errorf("admin request Authorization = %v, want the service account's token", got)
	}
	if fake.lastGrantType != "client_credentials" {
		t.Errorf("grant_type = %q, want client_credentials", fake.lastGrantType)
	}
}

// The decision path must not depend on the identity provider being up, so
// runtime roles come from the token the caller already presented.
func TestRuntimeRolesComeFromTheVerifiedTokenWithoutCallingKeycloak(t *testing.T) {
	fake := newFakeKeycloak(t)
	defer fake.Close()

	roles, err := fake.directory(t).ResolveRuntimeRoles(context.Background(), tokenverifier.VerifiedToken{
		Subject:  "user-doctor",
		TenantID: "tenant-a",
		Roles:    []string{"kc:cerbos-poc:patient-app:doctor"},
	}, tenant)
	if err != nil {
		t.Fatalf("ResolveRuntimeRoles: %v", err)
	}
	if len(roles) != 1 || roles[0] != "kc:cerbos-poc:patient-app:doctor" {
		t.Errorf("roles = %v, want the token's canonical roles", roles)
	}
	if got := fake.adminRequests.Load(); got != 0 {
		t.Errorf("the adapter made %d admin calls resolving runtime roles, want 0", got)
	}
}

// A token minted for one tenant must never resolve roles inside another.
func TestRuntimeRolesForATokenFromAnotherTenantAreRefused(t *testing.T) {
	fake := newFakeKeycloak(t)
	defer fake.Close()

	_, err := fake.directory(t).ResolveRuntimeRoles(context.Background(), tokenverifier.VerifiedToken{
		Subject:  "user-doctor",
		TenantID: "tenant-b",
		Roles:    []string{"kc:cerbos-poc:patient-app:doctor"},
	}, tenant)
	if !errors.Is(err, idpdirectory.ErrUnknownTenant) {
		t.Errorf("ResolveRuntimeRoles error = %v, want %v", err, idpdirectory.ErrUnknownTenant)
	}
}

// --- a fake Keycloak admin API ---------------------------------------------

type user struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
	Enabled   bool   `json:"enabled"`
}

type role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type fakeKeycloak struct {
	*httptest.Server
	t *testing.T

	users       []user
	clientRoles []role

	tokenRequests     atomic.Int64
	adminRequests     atomic.Int64
	lastAuthorization atomic.Pointer[string]
	lastGrantType     string

	queries map[string]url.Values
}

func newFakeKeycloak(t *testing.T) *fakeKeycloak {
	t.Helper()
	fake := &fakeKeycloak{t: t, queries: map[string]url.Values{}}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	return fake
}

func (f *fakeKeycloak) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	f.queries[path] = r.URL.Query()

	if path == "/realms/"+realm+"/protocol/openid-connect/token" {
		f.tokenRequests.Add(1)
		if err := r.ParseForm(); err != nil {
			f.t.Errorf("parsing the token request: %v", err)
		}
		f.lastGrantType = r.PostForm.Get("grant_type")
		writeJSON(f.t, w, map[string]any{
			"access_token": "service-account-token",
			"expires_in":   300,
			"token_type":   "Bearer",
		})
		return
	}

	f.adminRequests.Add(1)
	authorization := r.Header.Get("Authorization")
	f.lastAuthorization.Store(&authorization)

	switch {
	case path == "/admin/realms/"+realm+"/users":
		writeJSON(f.t, w, window(f.users, r))
	case strings.HasPrefix(path, "/admin/realms/"+realm+"/users/"):
		id := strings.TrimPrefix(path, "/admin/realms/"+realm+"/users/")
		for _, candidate := range f.users {
			if candidate.ID == id {
				writeJSON(f.t, w, candidate)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case path == "/admin/realms/"+realm+"/clients":
		writeJSON(f.t, w, []map[string]string{{"id": "client-uuid", "clientId": clientID}})
	case path == "/admin/realms/"+realm+"/clients/client-uuid/roles":
		writeJSON(f.t, w, window(f.clientRoles, r))
	case strings.HasPrefix(path, "/admin/realms/"+realm+"/clients/client-uuid/roles/"):
		name := strings.TrimPrefix(path, "/admin/realms/"+realm+"/clients/client-uuid/roles/")
		for _, candidate := range f.clientRoles {
			if candidate.Name == name || candidate.ID == name {
				writeJSON(f.t, w, candidate)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		f.t.Errorf("the adapter called an endpoint the fake does not model: %s", path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func window[T any](all []T, r *http.Request) []T {
	first, _ := strconv.Atoi(r.URL.Query().Get("first"))
	max, err := strconv.Atoi(r.URL.Query().Get("max"))
	if err != nil || max <= 0 {
		max = len(all)
	}
	if first > len(all) {
		return nil
	}
	end := min(first+max, len(all))
	return all[first:end]
}

func (f *fakeKeycloak) lastQuery(path string) url.Values {
	return f.queries[path]
}

func (f *fakeKeycloak) directory(t *testing.T) idpdirectory.IdentityDirectory {
	t.Helper()
	directory, err := keycloak.New(keycloak.Config{
		BaseURL:      f.URL,
		Realm:        realm,
		TenantID:     tenant,
		RoleSource:   tokenverifier.RoleSourceClient,
		ClientID:     clientID,
		ServiceUser:  "authorization-admin-service",
		ClientSecret: "a-secret-nobody-should-see",
	})
	if err != nil {
		t.Fatalf("keycloak.New: %v", err)
	}
	return directory
}

func canonicalRolesFromToken(t *testing.T, roleName string) []string {
	t.Helper()
	// Normalisation is the verifier's, so this cannot drift from what a real
	// token produces.
	return tokenverifier.CanonicalRoles(tokenverifier.Config{
		Realm:      realm,
		RoleSource: tokenverifier.RoleSourceClient,
		ClientID:   clientID,
	}, []string{roleName})
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encoding a response: %v", err)
	}
}
