package keycloakbulkload_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tishan-harischandra/cerbos-poc/libs/keycloakbulkload"
)

// This suite runs against a real Keycloak 26.4 backed by a real PostgreSQL -
// docker-compose.yml's keycloak-loadtest / keycloak-db pair, started with
// `docker compose --profile loadtest up --detach keycloak-db keycloak-loadtest`
// - because the whole point of this package is behaviour that only a real
// instance of exactly this schema can confirm: a row this package writes has
// to make Keycloak's own login path accept it.
const (
	adminURLEnv = "KEYCLOAK_LOADTEST_ADMIN_URL"
	dbDSNEnv    = "KEYCLOAK_LOADTEST_DB_DSN"
)

func skipUnlessConfigured(t *testing.T) (adminURL, dsn string) {
	t.Helper()
	adminURL = os.Getenv(adminURLEnv)
	dsn = os.Getenv(dbDSNEnv)
	if adminURL == "" || dsn == "" {
		t.Skipf("%s and %s are not both set", adminURLEnv, dbDSNEnv)
	}
	return adminURL, dsn
}

func TestBulkLoadedUsersCanLogInWithTheCorrectClientRoleClaim(t *testing.T) {
	adminURL, dsn := skipUnlessConfigured(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	realm := "kcbulkload-it-" + time.Now().UTC().Format("20060102150405")
	clientID := "patient-app"
	roleNames := []string{"doctor", "auditor", "clerk"}

	admin, err := keycloakbulkload.NewAdminClient(keycloakbulkload.AdminConfig{
		BaseURL:       adminURL,
		AdminUser:     "admin",
		AdminPassword: envOr("KEYCLOAK_ADMIN_PASSWORD", "change-me"),
	})
	if err != nil {
		t.Fatalf("NewAdminClient: %v", err)
	}

	clientUUID, roleIDByName, err := admin.EnsureRealm(ctx, keycloakbulkload.RealmSetup{
		Realm:          realm,
		ClientID:       clientID,
		RoleNames:      roleNames,
		PasswordPolicy: keycloakbulkload.LoadTestPasswordPolicy,
	})
	if err != nil {
		t.Fatalf("EnsureRealm: %v", err)
	}
	_ = clientUUID

	realmID, err := admin.RealmID(ctx, realm)
	if err != nil {
		t.Fatalf("RealmID: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to keycloak's database: %v", err)
	}
	defer pool.Close()

	cred, err := keycloakbulkload.NewSharedCredential("Load-Test-Only-P@ss1")
	if err != nil {
		t.Fatalf("NewSharedCredential: %v", err)
	}

	users := make(chan keycloakbulkload.UserRecord, 8)
	go func() {
		defer close(users)
		users <- keycloakbulkload.UserRecord{
			ID:         deterministicTestUUID(realm + ":user-0"),
			Username:   "bulkload-it-user-0",
			FirstName:  "Load",
			LastName:   "TestUser",
			Email:      "bulkload-it-user-0@example.test",
			TenantID:   "tenant-a",
			HospitalID: "hospital-1",
			RoleIDs:    []string{roleIDByName["doctor"], roleIDByName["auditor"]},
		}
	}()

	stats, err := keycloakbulkload.BulkLoad(ctx, pool, keycloakbulkload.LoadConfig{
		RealmID:    realmID,
		Credential: cred,
		Now:        time.Now().UTC(),
	}, users)
	if err != nil {
		t.Fatalf("BulkLoad: %v", err)
	}
	if stats.Users != 1 {
		t.Fatalf("stats.Users = %d, want 1", stats.Users)
	}
	if stats.RoleMappings != 2 {
		t.Fatalf("stats.RoleMappings = %d, want 2", stats.RoleMappings)
	}

	token, resourceAccess := passwordGrant(t, adminURL, realm, clientID, "bulkload-it-user-0", "Load-Test-Only-P@ss1")
	if token == "" {
		t.Fatal("password grant returned no access token")
	}
	roles := resourceAccess[clientID]
	if !containsAll(roles, "doctor", "auditor") {
		t.Errorf("resource_access[%s].roles = %v, want doctor and auditor", clientID, roles)
	}
}

func passwordGrant(t *testing.T, baseURL, realm, clientID, username, password string) (accessToken string, resourceAccess map[string][]string) {
	t.Helper()
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {clientID},
		"username":   {username},
		"password":   {password},
	}
	resp, err := http.Post(baseURL+"/realms/"+realm+"/protocol/openid-connect/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("password grant request: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding token response: %v", err)
	}
	if body.Error != "" {
		t.Fatalf("password grant failed: %s: %s", body.Error, body.Description)
	}

	claims := decodeJWTClaims(t, body.AccessToken)
	resourceAccess = make(map[string][]string)
	if raw, ok := claims["resource_access"].(map[string]any); ok {
		for client, v := range raw {
			if entry, ok := v.(map[string]any); ok {
				if rolesRaw, ok := entry["roles"].([]any); ok {
					for _, r := range rolesRaw {
						if s, ok := r.(string); ok {
							resourceAccess[client] = append(resourceAccess[client], s)
						}
					}
				}
			}
		}
	}
	return body.AccessToken, resourceAccess
}

func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("access token is not a JWT: %d parts", len(parts))
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		t.Fatalf("decoding JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshalling JWT claims: %v", err)
	}
	return claims
}

func containsAll(haystack []string, wants ...string) bool {
	for _, want := range wants {
		found := false
		for _, h := range haystack {
			if h == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
