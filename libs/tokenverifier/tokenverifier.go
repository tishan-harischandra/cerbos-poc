// Package tokenverifier turns a bearer token into a verified identity.
//
// §16.1 makes four checks mandatory - issuer, audience, expiry and signature -
// and requires that only *configured* role claims are normalised. This package
// is the single place that performs them, so the ADS never has to decide
// whether a request field can be trusted: if it did not come out of a
// VerifiedToken, it did not come from the identity provider.
//
// The synthetic role is the platform's to inject (§16.1, ADR-003). A token
// presenting one is rejected outright rather than filtered, because a caller
// who tried it is not a caller whose remaining claims are worth honouring.
package tokenverifier

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/canonicalid"
)

// The failures a caller may want to tell apart. They are deliberately coarse:
// an unauthenticated caller learns only that the token was refused, while the
// service log keeps the detail.
var (
	// ErrMalformedToken covers anything that is not three base64url segments
	// carrying JSON.
	ErrMalformedToken = errors.New("the token is malformed")
	// ErrUnexpectedAlgorithm guards the algorithm-confusion attack: a token
	// header naming `none`, or an HMAC algorithm verified against a public
	// key, must never be accepted.
	ErrUnexpectedAlgorithm = errors.New("the token is not signed with an accepted algorithm")
	// ErrUnknownKey means the header named a key the issuer does not publish.
	ErrUnknownKey = errors.New("the token was signed with an unknown key")
	// ErrInvalidSignature means the signature did not verify.
	ErrInvalidSignature = errors.New("the token signature is invalid")
	// ErrInvalidIssuer means the token came from somewhere else.
	ErrInvalidIssuer = errors.New("the token issuer is not the configured issuer")
	// ErrInvalidAudience means the token was minted for another client.
	ErrInvalidAudience = errors.New("the token audience does not include this service")
	// ErrExpired means the token is outside its validity window.
	ErrExpired = errors.New("the token has expired")
	// ErrNotYetValid means the token's nbf is in the future.
	ErrNotYetValid = errors.New("the token is not valid yet")
	// ErrReservedRole means the token claimed a role only the platform may
	// assign.
	ErrReservedRole = errors.New("the token carries a role reserved for the platform")
	// ErrMissingTenant means the token carries no tenant, so no server-side
	// tenant context could be derived.
	ErrMissingTenant = errors.New("the token carries no tenant")
)

// RoleSource names which Keycloak role claim is authoritative (§7.3).
type RoleSource string

const (
	// RoleSourceClient reads resource_access.<client-id>.roles.
	RoleSourceClient RoleSource = "CLIENT"
	// RoleSourceRealm reads realm_access.roles.
	RoleSourceRealm RoleSource = "REALM"
)

// TenantMappingMode names where the tenant comes from (§7.1).
type TenantMappingMode string

const (
	// TenantMappingClaim reads the configured tenant claim from the token.
	TenantMappingClaim TenantMappingMode = "CLAIM"
	// TenantMappingRealm uses the realm name as the tenant identifier.
	TenantMappingRealm TenantMappingMode = "REALM"
)

// Default claim names. Keycloak emits tenant and hospital through user
// attribute mappers, so the names are configurable rather than fixed.
const (
	DefaultTenantClaim   = "tenant_id"
	DefaultHospitalClaim = "hospital_id"
)

// DefaultLeeway absorbs clock drift between the identity provider and this
// service. Without it a correctly issued token is intermittently rejected on
// hosts whose clocks differ by a second, which looks like a flaky login.
const DefaultLeeway = 30 * time.Second

// KeySource supplies the issuer's signing keys, keyed by the `kid` header.
type KeySource interface {
	KeyByID(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Config describes the installation's identity provider (§7.1).
type Config struct {
	Issuer   string
	Audience string
	Realm    string

	RoleSource RoleSource
	// ClientID is the client whose roles are authoritative when RoleSource is
	// CLIENT. It is also the audience Keycloak stamps on the token.
	ClientID string

	TenantMappingMode TenantMappingMode
	TenantClaim       string
	HospitalClaim     string

	Keys   KeySource
	Now    func() time.Time
	Leeway time.Duration
}

// VerifiedToken is the identity the ADS is allowed to act on.
type VerifiedToken struct {
	Subject    string
	Username   string
	Issuer     string
	TenantID   string
	HospitalID string
	// Roles are canonical §7.5 identifiers, byte-identical to the ones the
	// identity directory reports for the same roles.
	Roles     []string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Verifier performs the §16.1 identity checks.
type Verifier struct {
	cfg Config
}

// New validates the configuration and returns a Verifier.
func New(cfg Config) (*Verifier, error) {
	switch {
	case cfg.Issuer == "":
		return nil, errors.New("tokenverifier: an issuer is required")
	case cfg.Audience == "":
		return nil, errors.New("tokenverifier: an audience is required")
	case cfg.Keys == nil:
		return nil, errors.New("tokenverifier: a key source is required")
	}

	if cfg.RoleSource == "" {
		cfg.RoleSource = RoleSourceClient
	}
	if cfg.RoleSource == RoleSourceClient && cfg.ClientID == "" {
		return nil, errors.New("tokenverifier: a client id is required when roles come from a client")
	}
	if cfg.TenantMappingMode == "" {
		cfg.TenantMappingMode = TenantMappingClaim
	}
	if cfg.TenantMappingMode == TenantMappingRealm && cfg.Realm == "" {
		return nil, errors.New("tokenverifier: a realm is required when the tenant is mapped from the realm")
	}
	if cfg.TenantClaim == "" {
		cfg.TenantClaim = DefaultTenantClaim
	}
	if cfg.HospitalClaim == "" {
		cfg.HospitalClaim = DefaultHospitalClaim
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Leeway <= 0 {
		cfg.Leeway = DefaultLeeway
	}

	return &Verifier{cfg: cfg}, nil
}

type jwtHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type jwtClaims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  audience `json:"aud"`
	Username  string   `json:"preferred_username"`
	ExpiresAt *int64   `json:"exp"`
	NotBefore *int64   `json:"nbf"`
	IssuedAt  *int64   `json:"iat"`

	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`

	// Everything else, so a configurable tenant or hospital claim can be read
	// without this struct having to name it.
	rest map[string]any
}

// audience decodes the JWT `aud` claim, which RFC 7519 allows to be either a
// string or an array of strings. Keycloak emits both shapes depending on how
// many audiences a client mapper adds.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = audience{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("the aud claim is neither a string nor an array: %w", err)
	}
	*a = many
	return nil
}

func (a audience) contains(want string) bool {
	for _, value := range a {
		if value == want {
			return true
		}
	}
	return false
}

// Verify performs every §16.1 identity check and returns the identity the token
// carries. A non-nil error means nothing in the token may be trusted.
func (v *Verifier) Verify(ctx context.Context, raw string) (VerifiedToken, error) {
	segments := strings.Split(strings.TrimSpace(raw), ".")
	if len(segments) != 3 {
		return VerifiedToken{}, ErrMalformedToken
	}

	header, err := decodeSegment[jwtHeader](segments[0])
	if err != nil {
		return VerifiedToken{}, ErrMalformedToken
	}
	// Signature before claims: an unverified payload is attacker-controlled
	// input, and reading it first invites decisions based on it.
	if header.Algorithm != "RS256" {
		return VerifiedToken{}, fmt.Errorf("%w: %q", ErrUnexpectedAlgorithm, header.Algorithm)
	}

	key, err := v.cfg.Keys.KeyByID(ctx, header.KeyID)
	if err != nil {
		return VerifiedToken{}, fmt.Errorf("%w: %v", ErrUnknownKey, err)
	}
	if key == nil {
		return VerifiedToken{}, ErrUnknownKey
	}

	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return VerifiedToken{}, ErrMalformedToken
	}
	digest := sha256.Sum256([]byte(segments[0] + "." + segments[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return VerifiedToken{}, ErrInvalidSignature
	}

	claims, err := decodeSegment[jwtClaims](segments[1])
	if err != nil {
		return VerifiedToken{}, ErrMalformedToken
	}
	if err := decodeRest(segments[1], &claims); err != nil {
		return VerifiedToken{}, ErrMalformedToken
	}

	if subtle.ConstantTimeCompare([]byte(claims.Issuer), []byte(v.cfg.Issuer)) != 1 {
		return VerifiedToken{}, fmt.Errorf("%w: %q", ErrInvalidIssuer, claims.Issuer)
	}
	if !claims.Audience.contains(v.cfg.Audience) {
		return VerifiedToken{}, fmt.Errorf("%w: %v", ErrInvalidAudience, []string(claims.Audience))
	}

	now := v.cfg.Now()
	if claims.ExpiresAt == nil {
		return VerifiedToken{}, fmt.Errorf("%w: the token carries no expiry", ErrExpired)
	}
	expiresAt := time.Unix(*claims.ExpiresAt, 0)
	if !now.Add(-v.cfg.Leeway).Before(expiresAt) {
		return VerifiedToken{}, ErrExpired
	}
	if claims.NotBefore != nil {
		notBefore := time.Unix(*claims.NotBefore, 0)
		if now.Add(v.cfg.Leeway).Before(notBefore) {
			return VerifiedToken{}, ErrNotYetValid
		}
	}

	roles, err := v.normaliseRoles(claims)
	if err != nil {
		return VerifiedToken{}, err
	}

	tenant, err := v.tenantOf(claims)
	if err != nil {
		return VerifiedToken{}, err
	}

	verified := VerifiedToken{
		Subject:    claims.Subject,
		Username:   claims.Username,
		Issuer:     claims.Issuer,
		TenantID:   tenant,
		HospitalID: stringClaim(claims.rest, v.cfg.HospitalClaim),
		Roles:      roles,
		ExpiresAt:  expiresAt,
	}
	if claims.IssuedAt != nil {
		verified.IssuedAt = time.Unix(*claims.IssuedAt, 0)
	}
	return verified, nil
}

// normaliseRoles reads only the configured role claim (§16.1) and renders each
// role as a canonical §7.5 identifier.
func (v *Verifier) normaliseRoles(claims jwtClaims) ([]string, error) {
	var raw []string
	switch v.cfg.RoleSource {
	case RoleSourceRealm:
		raw = claims.RealmAccess.Roles
	default:
		raw = claims.ResourceAccess[v.cfg.ClientID].Roles
	}

	// The reserved-role check runs over every claim the token carries, not
	// only the configured source. A caller smuggling `sys:` into the realm
	// claim while the installation reads client roles is still a caller
	// attempting to impersonate the platform.
	for _, role := range allRoles(claims) {
		if canonicalid.IsReserved(role) {
			return nil, fmt.Errorf("%w: %q", ErrReservedRole, role)
		}
	}

	return CanonicalRoles(v.cfg, raw), nil
}

// CanonicalRoles renders provider role names as canonical §7.5 identifiers.
//
// It is exported because the identity directory adapter has to produce exactly
// the same strings for the same roles, and §7.5 makes that agreement a
// requirement rather than a coincidence. Sharing this function is what makes it
// structural: there is no second implementation to drift.
func CanonicalRoles(cfg Config, names []string) []string {
	roles := make([]string, 0, len(names))
	for _, role := range names {
		if role == "" {
			continue
		}
		if cfg.RoleSource == RoleSourceRealm {
			roles = append(roles, canonicalid.KeycloakRealmRole(cfg.Realm, role))
			continue
		}
		roles = append(roles, canonicalid.KeycloakClientRole(cfg.Realm, cfg.ClientID, role))
	}
	return roles
}

func (v *Verifier) tenantOf(claims jwtClaims) (string, error) {
	if v.cfg.TenantMappingMode == TenantMappingRealm {
		return v.cfg.Realm, nil
	}
	tenant := stringClaim(claims.rest, v.cfg.TenantClaim)
	if tenant == "" {
		return "", ErrMissingTenant
	}
	return tenant, nil
}

func allRoles(claims jwtClaims) []string {
	roles := append([]string(nil), claims.RealmAccess.Roles...)
	for _, access := range claims.ResourceAccess {
		roles = append(roles, access.Roles...)
	}
	return roles
}

func stringClaim(rest map[string]any, name string) string {
	value, ok := rest[name].(string)
	if !ok {
		return ""
	}
	return value
}

func decodeSegment[T any](segment string) (T, error) {
	var value T
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(decoded, &value); err != nil {
		return value, err
	}
	return value, nil
}

func decodeRest(segment string, claims *jwtClaims) error {
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, &claims.rest)
}
