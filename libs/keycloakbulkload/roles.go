package keycloakbulkload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// AdminConfig points at the Keycloak Admin REST API of the instance this
// package's realm/client/role setup runs against (deploy/keycloak's
// keycloak-loadtest service, never the demo instance).
type AdminConfig struct {
	BaseURL       string
	AdminUser     string
	AdminPassword string
	HTTPClient    *http.Client
}

// AdminClient does the small, one-time part of standing up the load realm -
// creating the realm itself, the patient-app client and its 250-role catalog
// - through the ordinary, documented Admin REST API. Nothing about this
// client touches Keycloak's schema directly; only the bulk user/credential/
// role-mapping load in writer.go does.
type AdminClient struct {
	cfg  AdminConfig
	http *http.Client
}

// NewAdminClient validates cfg and returns a client.
func NewAdminClient(cfg AdminConfig) (*AdminClient, error) {
	switch {
	case cfg.BaseURL == "":
		return nil, fmt.Errorf("keycloakbulkload: a base URL is required")
	case cfg.AdminUser == "":
		return nil, fmt.Errorf("keycloakbulkload: an admin user is required")
	case cfg.AdminPassword == "":
		return nil, fmt.Errorf("keycloakbulkload: an admin password is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &AdminClient{cfg: cfg, http: client}, nil
}

// token requests a fresh master-realm admin token. Tokens are short-lived
// (Keycloak's dev default is 60s), so this is called once per admin call
// rather than cached - the setup this client does is a handful of calls, not
// a hot path.
func (c *AdminClient) token(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {"admin-cli"},
		"username":   {c.cfg.AdminUser},
		"password":   {c.cfg.AdminPassword},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/realms/master/protocol/openid-connect/token",
		bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloakbulkload: requesting an admin token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keycloakbulkload: admin token request failed with %d: %s", resp.StatusCode, body)
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("keycloakbulkload: decoding an admin token: %w", err)
	}
	return parsed.AccessToken, nil
}

func (c *AdminClient) call(ctx context.Context, method, path string, body any) (int, []byte, error) {
	token, err := c.token(ctx)
	if err != nil {
		return 0, nil, err
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("keycloakbulkload: encoding a request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("keycloakbulkload: calling %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

// RealmSetup describes the load realm this client stands up before the bulk
// load runs.
type RealmSetup struct {
	Realm          string
	ClientID       string
	RoleNames      []string
	PasswordPolicy string
}

// EnsureRealm creates the realm, the client and every canonical role in
// RoleNames if they do not already exist, and returns the client's internal
// UUID and a name-to-role-id map the bulk loader needs to write
// user_role_mapping rows. Idempotent: re-running against an already-set-up
// realm leaves it unchanged and fails on nothing that already exists.
func (c *AdminClient) EnsureRealm(ctx context.Context, setup RealmSetup) (clientUUID string, roleIDByName map[string]string, err error) {
	status, body, err := c.call(ctx, http.MethodPost, "/admin/realms", map[string]any{
		"realm":          setup.Realm,
		"enabled":        true,
		"passwordPolicy": setup.PasswordPolicy,
	})
	if err != nil {
		return "", nil, err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return "", nil, fmt.Errorf("keycloakbulkload: creating realm %q failed with %d: %s", setup.Realm, status, body)
	}

	status, body, err = c.call(ctx, http.MethodPost, "/admin/realms/"+setup.Realm+"/clients", map[string]any{
		"clientId":                  setup.ClientID,
		"publicClient":              true,
		"protocol":                  "openid-connect",
		"directAccessGrantsEnabled": true,
		// Mirrors deploy/keycloak/realm-cerbos-poc.json's "tenant"/"hospital"
		// mappers: without these, a seeded user's token carries roles but no
		// tenant_id/hospital_id, and the decision path this harness exists to
		// exercise at scale has nothing to key a tenant or hospital scope on.
		"protocolMappers": []map[string]any{
			{
				"name":           "tenant",
				"protocol":       "openid-connect",
				"protocolMapper": "oidc-usermodel-attribute-mapper",
				"config": map[string]any{
					"user.attribute":     "tenant_id",
					"claim.name":         "tenant_id",
					"jsonType.label":     "String",
					"access.token.claim": "true",
					"id.token.claim":     "true",
				},
			},
			{
				"name":           "hospital",
				"protocol":       "openid-connect",
				"protocolMapper": "oidc-usermodel-attribute-mapper",
				"config": map[string]any{
					"user.attribute":     "hospital_id",
					"claim.name":         "hospital_id",
					"jsonType.label":     "String",
					"access.token.claim": "true",
					"id.token.claim":     "true",
				},
			},
		},
	})
	if err != nil {
		return "", nil, err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return "", nil, fmt.Errorf("keycloakbulkload: creating client %q failed with %d: %s", setup.ClientID, status, body)
	}

	status, body, err = c.call(ctx, http.MethodGet,
		"/admin/realms/"+setup.Realm+"/clients?clientId="+url.QueryEscape(setup.ClientID), nil)
	if err != nil {
		return "", nil, err
	}
	if status != http.StatusOK {
		return "", nil, fmt.Errorf("keycloakbulkload: looking up client %q failed with %d: %s", setup.ClientID, status, body)
	}
	var clients []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &clients); err != nil {
		return "", nil, fmt.Errorf("keycloakbulkload: decoding client lookup: %w", err)
	}
	if len(clients) == 0 {
		return "", nil, fmt.Errorf("keycloakbulkload: client %q was created but cannot be found", setup.ClientID)
	}
	clientUUID = clients[0].ID

	roleIDByName = make(map[string]string, len(setup.RoleNames))
	for _, name := range setup.RoleNames {
		status, body, err := c.call(ctx, http.MethodPost,
			"/admin/realms/"+setup.Realm+"/clients/"+clientUUID+"/roles", map[string]any{"name": name})
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusCreated && status != http.StatusConflict {
			return "", nil, fmt.Errorf("keycloakbulkload: creating role %q failed with %d: %s", name, status, body)
		}

		status, body, err = c.call(ctx, http.MethodGet,
			"/admin/realms/"+setup.Realm+"/clients/"+clientUUID+"/roles/"+url.PathEscape(name), nil)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusOK {
			return "", nil, fmt.Errorf("keycloakbulkload: looking up role %q failed with %d: %s", name, status, body)
		}
		var role struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &role); err != nil {
			return "", nil, fmt.Errorf("keycloakbulkload: decoding role %q: %w", name, err)
		}
		roleIDByName[name] = role.ID
	}

	return clientUUID, roleIDByName, nil
}

// RealmID looks up the internal UUID Keycloak assigned to realmName, needed
// by the bulk loader's raw-SQL writes (user_entity.realm_id, keycloak_role's
// realm scoping).
func (c *AdminClient) RealmID(ctx context.Context, realmName string) (string, error) {
	status, body, err := c.call(ctx, http.MethodGet, "/admin/realms/"+realmName, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("keycloakbulkload: looking up realm %q failed with %d: %s", realmName, status, body)
	}
	var realm struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &realm); err != nil {
		return "", fmt.Errorf("keycloakbulkload: decoding realm %q: %w", realmName, err)
	}
	return realm.ID, nil
}
