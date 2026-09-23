// Package keycloak implements the identity directory port against Keycloak's
// Admin REST API (§7.3).
//
// Nothing outside the provider factory may import this package: consumers see
// only idpdirectory.IdentityDirectory, so replacing Keycloak is a configuration
// change. An architecture test enforces that.
//
// The adapter authenticates as a dedicated confidential service account and
// never accepts, forwards or echoes a browser's credentials (§7.3, §16.1).
package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// DefaultTimeout bounds one Admin REST call. The Admin Console is a human
// waiting on a page, so a hung directory has to fail rather than hang.
const DefaultTimeout = 10 * time.Second

// tokenRefreshMargin renews the service account token before it expires, so a
// call never fails on a token that expired mid-flight.
const tokenRefreshMargin = 30 * time.Second

// Config describes the Keycloak installation this adapter serves.
type Config struct {
	BaseURL string
	Realm   string
	// TenantID is the authorization tenant this realm maps to (§7.1's
	// tenantMappingMode: REALM).
	TenantID idpdirectory.TenantID

	RoleSource tokenverifier.RoleSource
	// ClientID is the client whose roles are authoritative when RoleSource is
	// CLIENT.
	ClientID string

	// ServiceUser is the confidential client the adapter authenticates as. It
	// is separate from ClientID: the browser-facing client must not hold admin
	// permissions.
	ServiceUser  string
	ClientSecret string

	HTTPClient *http.Client
	Now        func() time.Time
}

// Directory is the Keycloak identity directory adapter.
type Directory struct {
	cfg  Config
	http *http.Client

	mu            sync.Mutex
	token         string
	tokenExpires  time.Time
	clientUUID    string
	clientUUIDSet bool
}

// New validates the configuration and returns the adapter.
func New(cfg Config) (*Directory, error) {
	switch {
	case cfg.BaseURL == "":
		return nil, errors.New("keycloak: a base url is required")
	case cfg.Realm == "":
		return nil, errors.New("keycloak: a realm is required")
	case cfg.TenantID == "":
		return nil, errors.New("keycloak: a tenant is required")
	case cfg.ServiceUser == "":
		return nil, errors.New("keycloak: a service account client id is required")
	case cfg.ClientSecret == "":
		return nil, errors.New("keycloak: a service account secret is required")
	}
	if cfg.RoleSource == "" {
		cfg.RoleSource = tokenverifier.RoleSourceClient
	}
	switch cfg.RoleSource {
	case tokenverifier.RoleSourceClient, tokenverifier.RoleSourceRealm, tokenverifier.RoleSourceOrganization:
	default:
		return nil, fmt.Errorf("keycloak: unsupported role source %q; supported sources are CLIENT, REALM, and ORGANIZATION", cfg.RoleSource)
	}
	if (cfg.RoleSource == tokenverifier.RoleSourceClient || cfg.RoleSource == tokenverifier.RoleSourceOrganization) && cfg.ClientID == "" {
		return nil, errors.New("keycloak: a client id is required when roles come from a client or organization")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	return &Directory{cfg: cfg, http: client}, nil
}

// SearchUsers returns one window of the realm's users.
func (d *Directory) SearchUsers(ctx context.Context, tenant idpdirectory.TenantID, query idpdirectory.UserSearch) (idpdirectory.Page[idpdirectory.UserRef], error) {
	var empty idpdirectory.Page[idpdirectory.UserRef]
	if err := d.checkTenant(tenant); err != nil {
		return empty, err
	}

	page := query.Page.Normalised()
	params := url.Values{}
	params.Set("briefRepresentation", "true")
	if query.Query != "" {
		params.Set("search", query.Query)
	}
	applyWindow(params, page)

	var found []userRepresentation
	if err := d.getJSON(ctx, d.adminPath("users"), params, &found); err != nil {
		return empty, err
	}

	items, hasMore := trim(found, page.Limit)
	refs := make([]idpdirectory.UserRef, 0, len(items))
	for _, representation := range items {
		refs = append(refs, representation.toRef())
	}
	return idpdirectory.Page[idpdirectory.UserRef]{
		Items: refs, Offset: page.Offset, Limit: page.Limit, HasMore: hasMore,
	}, nil
}

// SearchRoles returns one window of the roles the installation treats as
// authoritative: realm roles or one client's roles, per §7.3.
func (d *Directory) SearchRoles(ctx context.Context, tenant idpdirectory.TenantID, query idpdirectory.RoleSearch) (idpdirectory.Page[idpdirectory.RoleRef], error) {
	var empty idpdirectory.Page[idpdirectory.RoleRef]
	if err := d.checkTenant(tenant); err != nil {
		return empty, err
	}

	page := query.Page.Normalised()
	params := url.Values{}
	if query.Query != "" {
		params.Set("search", query.Query)
	}

	if d.cfg.RoleSource == tokenverifier.RoleSourceOrganization {
		return d.searchOrganizationRoles(ctx, params, page)
	}

	applyWindow(params, page)
	var found []roleRepresentation
	if err := d.getRoles(ctx, "", params, &found); err != nil {
		return empty, err
	}

	items, hasMore := trim(found, page.Limit)
	refs := make([]idpdirectory.RoleRef, 0, len(items))
	for _, representation := range items {
		refs = append(refs, d.toRoleRef(representation))
	}
	return idpdirectory.Page[idpdirectory.RoleRef]{
		Items: refs, Offset: page.Offset, Limit: page.Limit, HasMore: hasMore,
	}, nil
}

// searchOrganizationRoles presents the tenant-wide catalog available when
// organization mode is active: realm roles followed by the configured browser
// client's roles. Both sources are read before the caller's page is applied so
// offsets and HasMore describe one deterministic catalog, not either backend
// endpoint in isolation.
func (d *Directory) searchOrganizationRoles(ctx context.Context, params url.Values, page idpdirectory.PageRequest) (idpdirectory.Page[idpdirectory.RoleRef], error) {
	realmRoles, err := d.readRealmRoles(ctx, params)
	if err != nil {
		return idpdirectory.Page[idpdirectory.RoleRef]{}, err
	}
	clientRoles, err := d.readClientRoles(ctx, params)
	if err != nil {
		return idpdirectory.Page[idpdirectory.RoleRef]{}, err
	}

	refsByCanonicalID := make(map[string]idpdirectory.RoleRef, len(realmRoles)+len(clientRoles))
	for _, sourced := range []struct {
		source tokenverifier.RoleSource
		roles  []roleRepresentation
	}{
		{source: tokenverifier.RoleSourceRealm, roles: realmRoles},
		{source: tokenverifier.RoleSourceClient, roles: clientRoles},
	} {
		for _, representation := range sourced.roles {
			ref := d.toRoleRefFrom(representation, sourced.source)
			refsByCanonicalID[ref.CanonicalID] = ref
		}
	}

	refs := make([]idpdirectory.RoleRef, 0, len(refsByCanonicalID))
	for _, ref := range refsByCanonicalID {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		iRealm := strings.Contains(refs[i].CanonicalID, ":realm:")
		jRealm := strings.Contains(refs[j].CanonicalID, ":realm:")
		if iRealm != jRealm {
			return iRealm
		}
		return refs[i].CanonicalID < refs[j].CanonicalID
	})

	start := min(page.Offset, len(refs))
	end := min(start+page.Limit, len(refs))
	return idpdirectory.Page[idpdirectory.RoleRef]{
		Items: refs[start:end], Offset: page.Offset, Limit: page.Limit, HasMore: end < len(refs),
	}, nil
}

// GetUser looks a user up by the stable identifier the authorization database
// persists.
func (d *Directory) GetUser(ctx context.Context, tenant idpdirectory.TenantID, externalID string) (idpdirectory.UserRef, error) {
	if err := d.checkTenant(tenant); err != nil {
		return idpdirectory.UserRef{}, err
	}
	var representation userRepresentation
	if err := d.getJSON(ctx, d.adminPath("users", externalID), nil, &representation); err != nil {
		return idpdirectory.UserRef{}, err
	}
	return representation.toRef(), nil
}

// GetRole looks a role up by name within the authoritative role source.
func (d *Directory) GetRole(ctx context.Context, tenant idpdirectory.TenantID, externalID string) (idpdirectory.RoleRef, error) {
	if err := d.checkTenant(tenant); err != nil {
		return idpdirectory.RoleRef{}, err
	}
	if d.cfg.RoleSource == tokenverifier.RoleSourceOrganization {
		var representation roleRepresentation
		err := d.getRealmRole(ctx, externalID, &representation)
		if err == nil {
			return d.toRoleRefFrom(representation, tokenverifier.RoleSourceRealm), nil
		}
		if !errors.Is(err, idpdirectory.ErrNotFound) {
			return idpdirectory.RoleRef{}, err
		}
		if err := d.getClientRole(ctx, externalID, &representation); err != nil {
			return idpdirectory.RoleRef{}, err
		}
		return d.toRoleRefFrom(representation, tokenverifier.RoleSourceClient), nil
	}

	var representation roleRepresentation
	if err := d.getRoles(ctx, externalID, nil, &representation); err != nil {
		return idpdirectory.RoleRef{}, err
	}
	return d.toRoleRef(representation), nil
}

// GetUserRoles reports the canonical roles directly assigned to a user
// from the authoritative role source (§7.3): a client's roles or the
// realm's, matching whichever getRoles already reads.
func (d *Directory) GetUserRoles(ctx context.Context, tenant idpdirectory.TenantID, userExternalID string) ([]idpdirectory.RoleRef, error) {
	if err := d.checkTenant(tenant); err != nil {
		return nil, err
	}

	path, err := d.userRoleMappingsPath(ctx, userExternalID)
	if err != nil {
		return nil, err
	}
	var found []roleRepresentation
	if err := d.getJSON(ctx, path, nil, &found); err != nil {
		return nil, err
	}

	refs := make([]idpdirectory.RoleRef, 0, len(found))
	for _, representation := range found {
		refs = append(refs, d.toRoleRef(representation))
	}
	return refs, nil
}

// userRoleMappingsPath resolves where one user's role mappings live,
// reusing clientRolesPath's client-id-to-UUID resolution and cache rather than
// re-deriving it.
func (d *Directory) userRoleMappingsPath(ctx context.Context, userExternalID string) (string, error) {
	if d.cfg.RoleSource == tokenverifier.RoleSourceRealm {
		return d.adminPath("users", userExternalID, "role-mappings", "realm"), nil
	}
	if _, err := d.clientRolesPath(ctx); err != nil {
		return "", err
	}
	d.mu.Lock()
	clientUUID := d.clientUUID
	d.mu.Unlock()
	return d.adminPath("users", userExternalID, "role-mappings", "clients", clientUUID), nil
}

// ResolveRuntimeRoles reports the canonical roles the verified token carries.
//
// It deliberately makes no directory call: the token has already been verified
// against the issuer's keys, and querying Keycloak on every decision would put
// the identity provider's availability and latency in front of every
// authorization call.
func (d *Directory) ResolveRuntimeRoles(_ context.Context, token tokenverifier.VerifiedToken, tenant idpdirectory.TenantID) ([]string, error) {
	if err := d.checkTenant(tenant); err != nil {
		return nil, err
	}
	if token.TenantID != string(tenant) {
		return nil, fmt.Errorf("%w: the token is for tenant %q", idpdirectory.ErrUnknownTenant, token.TenantID)
	}
	return append([]string(nil), token.Roles...), nil
}

// OrganizationsOfTenant returns one window of the realm's organizations
// (issue #85). Keycloak's own Organizations admin API (`GET
// /organizations`) is a read; nothing in this adapter ever writes one.
func (d *Directory) OrganizationsOfTenant(ctx context.Context, tenant idpdirectory.TenantID, query idpdirectory.OrganizationSearch) (idpdirectory.Page[idpdirectory.OrganizationRef], error) {
	var empty idpdirectory.Page[idpdirectory.OrganizationRef]
	if err := d.checkTenant(tenant); err != nil {
		return empty, err
	}

	page := query.Page.Normalised()
	params := url.Values{}
	if query.Query != "" {
		params.Set("search", query.Query)
	}
	applyWindow(params, page)

	var found []organizationRepresentation
	if err := d.getJSON(ctx, d.adminPath("organizations"), params, &found); err != nil {
		return empty, err
	}

	items, hasMore := trim(found, page.Limit)
	refs := make([]idpdirectory.OrganizationRef, 0, len(items))
	for _, representation := range items {
		refs = append(refs, representation.toRef())
	}
	return idpdirectory.Page[idpdirectory.OrganizationRef]{
		Items: refs, Offset: page.Offset, Limit: page.Limit, HasMore: hasMore,
	}, nil
}

// OrganizationsOfUser returns every organization a user belongs to
// (issue #85), via Keycloak's `GET
// /organizations/members/{member-id}/organizations`.
func (d *Directory) OrganizationsOfUser(ctx context.Context, tenant idpdirectory.TenantID, userExternalID string) ([]idpdirectory.OrganizationRef, error) {
	if err := d.checkTenant(tenant); err != nil {
		return nil, err
	}
	var found []organizationRepresentation
	if err := d.getJSON(ctx, d.adminPath("organizations", "members", userExternalID, "organizations"), nil, &found); err != nil {
		return nil, err
	}
	refs := make([]idpdirectory.OrganizationRef, 0, len(found))
	for _, representation := range found {
		refs = append(refs, representation.toRef())
	}
	return refs, nil
}

// MembersOfOrganization returns one window of an organization's members
// (issue #85), via Keycloak's `GET /organizations/{id}/members`.
func (d *Directory) MembersOfOrganization(ctx context.Context, tenant idpdirectory.TenantID, organizationExternalID string, page idpdirectory.PageRequest) (idpdirectory.Page[idpdirectory.UserRef], error) {
	var empty idpdirectory.Page[idpdirectory.UserRef]
	if err := d.checkTenant(tenant); err != nil {
		return empty, err
	}

	normalised := page.Normalised()
	params := url.Values{}
	applyWindow(params, normalised)

	var found []userRepresentation
	if err := d.getJSON(ctx, d.adminPath("organizations", organizationExternalID, "members"), params, &found); err != nil {
		return empty, err
	}

	items, hasMore := trim(found, normalised.Limit)
	refs := make([]idpdirectory.UserRef, 0, len(items))
	for _, representation := range items {
		refs = append(refs, representation.toRef())
	}
	return idpdirectory.Page[idpdirectory.UserRef]{
		Items: refs, Offset: normalised.Offset, Limit: normalised.Limit, HasMore: hasMore,
	}, nil
}

func (d *Directory) checkTenant(tenant idpdirectory.TenantID) error {
	if tenant != d.cfg.TenantID {
		return fmt.Errorf("%w: %q", idpdirectory.ErrUnknownTenant, tenant)
	}
	return nil
}

func (d *Directory) toRoleRef(representation roleRepresentation) idpdirectory.RoleRef {
	return d.toRoleRefFrom(representation, d.cfg.RoleSource)
}

func (d *Directory) toRoleRefFrom(representation roleRepresentation, source tokenverifier.RoleSource) idpdirectory.RoleRef {
	canonical := tokenverifier.CanonicalRoles(tokenverifier.Config{
		Realm:      d.cfg.Realm,
		RoleSource: source,
		ClientID:   d.cfg.ClientID,
	}, []string{representation.Name})

	ref := idpdirectory.RoleRef{
		ExternalID:  representation.ID,
		Name:        representation.Name,
		Description: representation.Description,
	}
	if len(canonical) == 1 {
		ref.CanonicalID = canonical[0]
	}
	return ref
}

func (d *Directory) readRealmRoles(ctx context.Context, params url.Values) ([]roleRepresentation, error) {
	var found []roleRepresentation
	if err := d.getJSON(ctx, d.adminPath("roles"), params, &found); err != nil {
		return nil, err
	}
	return found, nil
}

func (d *Directory) readClientRoles(ctx context.Context, params url.Values) ([]roleRepresentation, error) {
	var found []roleRepresentation
	if err := d.getClientRoles(ctx, "", params, &found); err != nil {
		return nil, err
	}
	return found, nil
}

func (d *Directory) getRealmRole(ctx context.Context, roleName string, into any) error {
	return d.getJSON(ctx, d.adminPath("roles", roleName), nil, into)
}

func (d *Directory) getClientRole(ctx context.Context, roleName string, into any) error {
	return d.getClientRoles(ctx, roleName, nil, into)
}

// getRoles preserves the legacy single-source fast paths. Organization mode
// uses the explicit realm and client helpers above so source information is not
// lost while the catalogs are merged.
func (d *Directory) getRoles(ctx context.Context, roleName string, params url.Values, into any) error {
	if d.cfg.RoleSource == tokenverifier.RoleSourceRealm {
		path := d.adminPath("roles")
		if roleName != "" {
			path += "/" + url.PathEscape(roleName)
		}
		return d.getJSON(ctx, path, params, into)
	}
	return d.getClientRoles(ctx, roleName, params, into)
}

// getClientRoles retries once against a freshly resolved client. The client's
// internal id is cached realm configuration, but a rebuilt realm can replace
// that id while this process is running.
func (d *Directory) getClientRoles(ctx context.Context, roleName string, params url.Values, into any) error {
	for attempt := range 2 {
		path, err := d.clientRolesPath(ctx)
		if err != nil {
			return err
		}
		if roleName != "" {
			path += "/" + url.PathEscape(roleName)
		}

		err = d.getJSON(ctx, path, params, into)
		if err == nil || attempt == 1 || !errors.Is(err, idpdirectory.ErrNotFound) {
			return err
		}
		d.forgetClient()
	}
	return nil
}

// forgetClient drops the cached client id so the next call resolves it again.
func (d *Directory) forgetClient() {
	d.mu.Lock()
	d.clientUUID, d.clientUUIDSet = "", false
	d.mu.Unlock()
}

// clientRolesPath resolves where the configured client's roles live,
// translating its public id into the internal UUID the Admin API needs. The
// translation is cached: it is realm configuration, not per-request data.
func (d *Directory) clientRolesPath(ctx context.Context) (string, error) {
	d.mu.Lock()
	cached, ok := d.clientUUID, d.clientUUIDSet
	d.mu.Unlock()
	if ok {
		return d.adminPath("clients", cached, "roles"), nil
	}

	params := url.Values{}
	params.Set("clientId", d.cfg.ClientID)
	var clients []struct {
		ID string `json:"id"`
	}
	if err := d.getJSON(ctx, d.adminPath("clients"), params, &clients); err != nil {
		return "", err
	}
	if len(clients) == 0 {
		return "", fmt.Errorf("%w: no client %q in realm %q",
			idpdirectory.ErrNotFound, d.cfg.ClientID, d.cfg.Realm)
	}

	d.mu.Lock()
	d.clientUUID, d.clientUUIDSet = clients[0].ID, true
	d.mu.Unlock()

	return d.adminPath("clients", clients[0].ID, "roles"), nil
}

func (d *Directory) adminPath(segments ...string) string {
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	return fmt.Sprintf("%s/admin/realms/%s/%s",
		d.cfg.BaseURL, url.PathEscape(d.cfg.Realm), strings.Join(escaped, "/"))
}

func (d *Directory) getJSON(ctx context.Context, endpoint string, params url.Values, into any) error {
	token, err := d.serviceAccountToken(ctx)
	if err != nil {
		return err
	}

	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("building a directory request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")

	response, err := d.http.Do(request)
	if err != nil {
		return fmt.Errorf("calling the identity directory: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return idpdirectory.ErrNotFound
	default:
		// The body may echo the request, so it is not repeated here: a
		// directory error must never become a channel for credentials.
		return fmt.Errorf("the identity directory answered %s", response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("reading the directory response: %w", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decoding the directory response: %w", err)
	}
	return nil
}

// serviceAccountToken returns a client-credentials token, minting a new one
// only when the cached one is about to expire.
func (d *Directory) serviceAccountToken(ctx context.Context) (string, error) {
	now := d.cfg.Now()

	d.mu.Lock()
	if d.token != "" && now.Add(tokenRefreshMargin).Before(d.tokenExpires) {
		token := d.token
		d.mu.Unlock()
		return token, nil
	}
	d.mu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", d.cfg.ServiceUser)
	form.Set("client_secret", d.cfg.ClientSecret)

	endpoint := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token",
		d.cfg.BaseURL, url.PathEscape(d.cfg.Realm))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building the service account token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := d.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("authenticating the service account: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		// Deliberately status only: the request carried the secret, and some
		// providers echo the form back in the error body.
		return "", fmt.Errorf("authenticating the service account: the identity provider answered %s",
			response.Status)
	}

	var grant struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&grant); err != nil {
		return "", fmt.Errorf("decoding the service account grant: %w", err)
	}
	if grant.AccessToken == "" {
		return "", errors.New("the identity provider returned no service account token")
	}

	d.mu.Lock()
	d.token = grant.AccessToken
	d.tokenExpires = now.Add(time.Duration(grant.ExpiresIn) * time.Second)
	d.mu.Unlock()

	return grant.AccessToken, nil
}

// applyWindow asks for one row more than the caller wants. That extra row is
// how HasMore is answered without a second count query, which on a realm with
// hundreds of thousands of users is the expensive one.
func applyWindow(params url.Values, page idpdirectory.PageRequest) {
	params.Set("first", strconv.Itoa(page.Offset))
	params.Set("max", strconv.Itoa(page.Limit+1))
}

func trim[T any](found []T, limit int) ([]T, bool) {
	if len(found) > limit {
		return found[:limit], true
	}
	return found, false
}

type userRepresentation struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
	Enabled   bool   `json:"enabled"`
}

func (u userRepresentation) toRef() idpdirectory.UserRef {
	display := strings.TrimSpace(strings.Join([]string{u.FirstName, u.LastName}, " "))
	if display == "" {
		display = u.Username
	}
	return idpdirectory.UserRef{
		ExternalID:  u.ID,
		Username:    u.Username,
		DisplayName: display,
		Email:       u.Email,
		Enabled:     u.Enabled,
	}
}

type roleRepresentation struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type organizationRepresentation struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Alias string `json:"alias"`
}

func (o organizationRepresentation) toRef() idpdirectory.OrganizationRef {
	return idpdirectory.OrganizationRef{ExternalID: o.ID, Alias: o.Alias, Name: o.Name}
}
