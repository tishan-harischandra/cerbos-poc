// Package provider is the composition root for identity provider adapters.
//
// It is the only package permitted to import a concrete adapter (§7.1: "only
// one provider adapter is active for an installation", and the domain APIs stay
// provider-neutral). Consumers take an idpdirectory.IdentityDirectory and a
// tokenverifier.Verifier from here and never learn which product is behind
// them, so switching provider is an environment change. An architecture test
// fails the build if any other package reaches past this one.
package provider

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/keycloak"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/wso2"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// Type names an identity provider product (§7.1).
type Type string

const (
	// TypeKeycloak selects the Keycloak Admin REST adapter.
	TypeKeycloak Type = "KEYCLOAK"
	// TypeWSO2 selects the WSO2 Identity Server adapter.
	TypeWSO2 Type = "WSO2_IS"
)

// Secret holds a machine credential. It refuses to render itself so a stray
// log line, an error body or a serialised configuration cannot become the way
// the IdP admin credential reaches a browser (§16.1, §7.4).
type Secret string

// String reports a placeholder, never the credential.
func (s Secret) String() string { return "REDACTED" }

// MarshalJSON reports a placeholder, never the credential.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"REDACTED"`), nil }

// Reveal returns the credential. The name is deliberately conspicuous: every
// call site is somewhere a reviewer should look.
func (s Secret) Reveal() string { return string(s) }

// Config is the §7.1 installation selection, resolved.
type Config struct {
	Type    Type
	BaseURL string
	Realm   string
	// TenantID is the authorization tenant this realm or organisation maps to.
	TenantID idpdirectory.TenantID
	// TenantMappingMode decides whether the tenant comes from a token claim or
	// from the realm itself.
	TenantMappingMode tokenverifier.TenantMappingMode

	RoleSource tokenverifier.RoleSource
	// ClientID is the browser-facing client: the token audience, and the owner
	// of the authoritative roles when RoleSource is CLIENT.
	ClientID string
	// ServiceClientID is the confidential service account the directory
	// adapter authenticates as. It is separate from ClientID so the
	// browser-facing client never needs admin permissions (§7.3).
	ServiceClientID string
	ClientSecret    Secret

	// Issuer is what a token must claim. It is derived from the base URL and
	// realm unless overridden, because a hand-written issuer that disagrees
	// with the base URL rejects every token.
	Issuer string
	// WSO2TenantDomain is WSO2's own tenant name (§7.4).
	WSO2TenantDomain string
}

// JWKSURL is where the issuer publishes its signing keys.
//
// It is built from the base URL rather than from the issuer. The two differ
// whenever a browser and a backend reach the identity provider by different
// names - which is the normal case, and is exactly the deployment where using
// the issuer here would send a backend to an address only the browser can
// resolve.
func (c Config) JWKSURL() string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", c.BaseURL, c.Realm)
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// Environment variables, all prefixed IDP_ so one glance at a compose file
// shows the whole identity configuration.
const (
	EnvType              = "IDP_TYPE"
	EnvBaseURL           = "IDP_BASE_URL"
	EnvRealm             = "IDP_REALM"
	EnvTenantID          = "IDP_TENANT_ID"
	EnvTenantMappingMode = "IDP_TENANT_MAPPING_MODE"
	EnvRoleSource        = "IDP_ROLE_SOURCE"
	EnvClientID          = "IDP_CLIENT_ID"
	EnvServiceClientID   = "IDP_SERVICE_CLIENT_ID"
	// EnvCredentialsSecretRef is §7.1's credentialsSecretRef: a path to a
	// mounted file. A secret passed by value would be visible in
	// `docker inspect` and inherited by every child process.
	EnvCredentialsSecretRef = "IDP_CREDENTIALS_SECRET_REF"
	EnvIssuer               = "IDP_ISSUER"
	EnvWSO2TenantDomain     = "IDP_WSO2_TENANT_DOMAIN"
)

// FromEnv resolves the installation's identity configuration.
func FromEnv(lookup LookupFunc) (Config, error) {
	cfg := Config{
		Type:              Type(valueOr(lookup, EnvType, string(TypeKeycloak))),
		BaseURL:           strings.TrimRight(valueOr(lookup, EnvBaseURL, ""), "/"),
		Realm:             valueOr(lookup, EnvRealm, ""),
		TenantID:          idpdirectory.TenantID(valueOr(lookup, EnvTenantID, "")),
		TenantMappingMode: tokenverifier.TenantMappingMode(valueOr(lookup, EnvTenantMappingMode, string(tokenverifier.TenantMappingClaim))),
		RoleSource:        tokenverifier.RoleSource(valueOr(lookup, EnvRoleSource, string(tokenverifier.RoleSourceClient))),
		ClientID:          valueOr(lookup, EnvClientID, ""),
		ServiceClientID:   valueOr(lookup, EnvServiceClientID, ""),
		Issuer:            strings.TrimRight(valueOr(lookup, EnvIssuer, ""), "/"),
		WSO2TenantDomain:  valueOr(lookup, EnvWSO2TenantDomain, ""),
	}

	switch {
	case cfg.BaseURL == "":
		return Config{}, fmt.Errorf("%s is required", EnvBaseURL)
	case cfg.Realm == "":
		return Config{}, fmt.Errorf("%s is required", EnvRealm)
	case cfg.TenantID == "":
		return Config{}, fmt.Errorf("%s is required", EnvTenantID)
	case cfg.ClientID == "":
		return Config{}, fmt.Errorf("%s is required", EnvClientID)
	}

	reference, ok := lookup(EnvCredentialsSecretRef)
	if !ok || reference == "" {
		return Config{}, fmt.Errorf("%s is required: it names the file the service account secret is mounted at", EnvCredentialsSecretRef)
	}
	return finish(cfg, reference)
}

// Tenant is the identity fields the provider package needs for one tenant
// registry row (issue #76): what libs/tenantregistry validates and
// assignmentstore.Store.Tenant reads back. Defined here rather than
// importing assignmentstore, so this package - the composition root for
// adapters - never becomes a database client too.
type Tenant struct {
	Realm               string
	Issuer              string
	BrowserClientID     string
	ServiceClientID     string
	CredentialSecretRef string
}

// ConfigFromTenant builds a Config from one tenant registry row (issue
// #76) instead of IDP_REALM/IDP_TENANT_ID/IDP_ISSUER/IDP_CLIENT_ID/
// IDP_SERVICE_CLIENT_ID/IDP_CREDENTIALS_SECRET_REF. Adapter-level settings
// that describe how this installation talks to the identity provider
// product - Type, BaseURL, TenantMappingMode, RoleSource,
// WSO2TenantDomain - still come from the environment: they are not who the
// tenant is, so they do not belong in the registry.
//
// TenantID is the realm verbatim (§7.1): there is no separate tenant_id
// column and no mapping layer.
func ConfigFromTenant(lookup LookupFunc, tenant Tenant) (Config, error) {
	cfg := Config{
		Type:              Type(valueOr(lookup, EnvType, string(TypeKeycloak))),
		BaseURL:           strings.TrimRight(valueOr(lookup, EnvBaseURL, ""), "/"),
		Realm:             tenant.Realm,
		TenantID:          idpdirectory.TenantID(tenant.Realm),
		TenantMappingMode: tokenverifier.TenantMappingMode(valueOr(lookup, EnvTenantMappingMode, string(tokenverifier.TenantMappingClaim))),
		RoleSource:        tokenverifier.RoleSource(valueOr(lookup, EnvRoleSource, string(tokenverifier.RoleSourceClient))),
		ClientID:          tenant.BrowserClientID,
		ServiceClientID:   tenant.ServiceClientID,
		Issuer:            strings.TrimRight(tenant.Issuer, "/"),
		WSO2TenantDomain:  valueOr(lookup, EnvWSO2TenantDomain, ""),
	}

	switch {
	case cfg.BaseURL == "":
		return Config{}, fmt.Errorf("%s is required", EnvBaseURL)
	case cfg.Realm == "":
		return Config{}, errors.New("the tenant registry row has no realm")
	case cfg.ClientID == "":
		return Config{}, errors.New("the tenant registry row has no browser client id")
	}

	if tenant.CredentialSecretRef == "" {
		return Config{}, errors.New("the tenant registry row has no credential secret ref")
	}
	return finish(cfg, tenant.CredentialSecretRef)
}

// finish reads the service account secret and applies the defaults common
// to both FromEnv and ConfigFromTenant.
func finish(cfg Config, credentialSecretRef string) (Config, error) {
	contents, err := os.ReadFile(credentialSecretRef)
	if err != nil {
		return Config{}, fmt.Errorf("reading the identity provider credentials from %s: %w", credentialSecretRef, err)
	}
	// Trailing whitespace is what an editor or `echo` leaves behind, and a
	// secret that fails only when written by hand is an unfindable bug.
	cfg.ClientSecret = Secret(strings.TrimSpace(string(contents)))
	if cfg.ClientSecret == "" {
		return Config{}, errors.New("the identity provider credentials file is empty")
	}

	if cfg.Issuer == "" {
		cfg.Issuer = fmt.Sprintf("%s/realms/%s", cfg.BaseURL, cfg.Realm)
	}
	if cfg.ServiceClientID == "" {
		cfg.ServiceClientID = cfg.ClientID
	}
	return cfg, nil
}

// New builds the one identity directory this installation uses.
func New(cfg Config) (idpdirectory.IdentityDirectory, error) {
	switch cfg.Type {
	case TypeKeycloak:
		return keycloak.New(keycloak.Config{
			BaseURL:      cfg.BaseURL,
			Realm:        cfg.Realm,
			TenantID:     cfg.TenantID,
			RoleSource:   cfg.RoleSource,
			ClientID:     cfg.ClientID,
			ServiceUser:  cfg.ServiceClientID,
			ClientSecret: cfg.ClientSecret.Reveal(),
		})
	case TypeWSO2:
		return wso2.New(wso2.Config{
			BaseURL:      cfg.BaseURL,
			TenantDomain: cfg.WSO2TenantDomain,
			TenantID:     cfg.TenantID,
			ServiceUser:  cfg.ServiceClientID,
			ClientSecret: cfg.ClientSecret.Reveal(),
		})
	default:
		return nil, fmt.Errorf("%s=%q names no adapter; supported types are %s and %s",
			EnvType, cfg.Type, TypeKeycloak, TypeWSO2)
	}
}

// NewVerifier builds the token verifier from the same configuration as the
// directory, so an installation cannot verify tokens for one realm while
// reading roles from another.
func NewVerifier(cfg Config) (*tokenverifier.Verifier, error) {
	return tokenverifier.New(tokenverifier.Config{
		Issuer:            cfg.Issuer,
		Audience:          cfg.ClientID,
		Realm:             cfg.Realm,
		RoleSource:        cfg.RoleSource,
		ClientID:          cfg.ClientID,
		TenantMappingMode: cfg.TenantMappingMode,
		Keys:              tokenverifier.NewJWKS(tokenverifier.JWKSConfig{URL: cfg.JWKSURL()}),
	})
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}
