package tokenverifier_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

const (
	issuer   = "http://keycloak:8080/realms/cerbos-poc"
	audience = "patient-app"
	realm    = "cerbos-poc"
)

// A token minted by the realm, for this audience, still inside its lifetime is
// the only shape that yields an identity. Everything the ADS later trusts -
// principal, tenant, hospital, roles - comes from here and from nowhere the
// browser can reach.
func TestAWellFormedTokenYieldsTheIdentityItCarries(t *testing.T) {
	fixture := newFixture(t)

	verified, err := fixture.verifier(t).Verify(context.Background(), fixture.sign(t, fixture.valid(nil)))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if verified.Subject != "user-doctor" {
		t.Errorf("Subject = %q, want %q", verified.Subject, "user-doctor")
	}
	if verified.Username != "doctor" {
		t.Errorf("Username = %q, want %q", verified.Username, "doctor")
	}
	if verified.TenantID != "tenant-a" {
		t.Errorf("TenantID = %q, want %q", verified.TenantID, "tenant-a")
	}
	if verified.HospitalID != "hospital-1" {
		t.Errorf("HospitalID = %q, want %q", verified.HospitalID, "hospital-1")
	}
	if len(verified.Roles) != 1 || verified.Roles[0] != "kc:cerbos-poc:patient-app:doctor" {
		t.Errorf("Roles = %v, want [kc:cerbos-poc:patient-app:doctor]", verified.Roles)
	}
}

// The four mandatory §16.1 checks, each proven by the token that fails only it.
// The valid token above is the control: every case below differs from it in one
// respect, so a pass here cannot come from some unrelated rejection.
func TestTheMandatoryIdentityChecksEachRejectATokenThatFailsThem(t *testing.T) {
	fixture := newFixture(t)
	otherKey := newFixture(t)

	tests := []struct {
		name  string
		token func() string
		want  error
	}{
		{
			name: "another issuer",
			token: func() string {
				return fixture.sign(t, fixture.valid(claims{"iss": "http://evil.example/realms/cerbos-poc"}))
			},
			want: tokenverifier.ErrInvalidIssuer,
		},
		{
			name: "another audience",
			token: func() string {
				return fixture.sign(t, fixture.valid(claims{"aud": "some-other-client"}))
			},
			want: tokenverifier.ErrInvalidAudience,
		},
		{
			name: "an expiry in the past",
			token: func() string {
				return fixture.sign(t, fixture.valid(claims{
					"exp": fixture.now.Add(-time.Hour).Unix(),
				}))
			},
			want: tokenverifier.ErrExpired,
		},
		{
			name: "a signature from a key the issuer does not publish",
			token: func() string {
				// Same kid, different key: the verifier finds a key and has
				// to reject on the signature rather than on the lookup.
				return signWith(t, otherKey.key, "test-key", "RS256", fixture.valid(nil))
			},
			want: tokenverifier.ErrInvalidSignature,
		},
		{
			name: "a key id the issuer does not publish",
			token: func() string {
				return signWith(t, fixture.key, "rotated-away", "RS256", fixture.valid(nil))
			},
			want: tokenverifier.ErrUnknownKey,
		},
		{
			name: "an algorithm nobody agreed to",
			token: func() string {
				return signWith(t, fixture.key, "test-key", "none", fixture.valid(nil))
			},
			want: tokenverifier.ErrUnexpectedAlgorithm,
		},
		{
			name:  "something that is not a token at all",
			token: func() string { return "not-a-token" },
			want:  tokenverifier.ErrMalformedToken,
		},
		{
			name: "no tenant to derive a server-side context from",
			token: func() string {
				payload := fixture.valid(nil)
				delete(payload, "tenant_id")
				return fixture.sign(t, payload)
			},
			want: tokenverifier.ErrMissingTenant,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.verifier(t).Verify(context.Background(), test.token())
			if !errors.Is(err, test.want) {
				t.Errorf("Verify error = %v, want %v", err, test.want)
			}
		})
	}
}

// The synthetic role is added only by the trusted ADS (§16.1). A token
// presenting one is refused outright - not filtered, because the rest of a
// forged claim set is not worth honouring.
func TestATokenCarryingAReservedRoleIsRejectedOutright(t *testing.T) {
	fixture := newFixture(t)

	smuggled := []struct {
		name    string
		payload claims
	}{
		{
			name: "in the configured client role claim",
			payload: claims{"resource_access": map[string]any{
				"patient-app": map[string]any{"roles": []string{"doctor", "sys:permission-evaluator"}},
			}},
		},
		{
			// The installation reads client roles, so this claim is not even
			// normalised. It is still an impersonation attempt.
			name:    "in a claim this installation does not read",
			payload: claims{"realm_access": map[string]any{"roles": []string{"sys:permission-evaluator"}}},
		},
		{
			name: "spelled in a different case",
			payload: claims{"resource_access": map[string]any{
				"patient-app": map[string]any{"roles": []string{"SYS:Permission-Evaluator"}},
			}},
		},
		{
			name: "under another client entirely",
			payload: claims{"resource_access": map[string]any{
				"patient-app": map[string]any{"roles": []string{"doctor"}},
				"another-app": map[string]any{"roles": []string{"sys:anything"}},
			}},
		},
	}

	for _, test := range smuggled {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.verifier(t).Verify(context.Background(),
				fixture.sign(t, fixture.valid(test.payload)))
			if !errors.Is(err, tokenverifier.ErrReservedRole) {
				t.Errorf("Verify error = %v, want %v", err, tokenverifier.ErrReservedRole)
			}
		})
	}
}

// §16.1 says normalise *only* the configured role claims. An installation
// reading client roles must not silently pick up realm roles as well, or a role
// granted for an unrelated application becomes an authorization input.
func TestOnlyTheConfiguredRoleClaimIsNormalised(t *testing.T) {
	fixture := newFixture(t)

	payload := fixture.valid(claims{
		"realm_access": map[string]any{"roles": []string{"offline_access", "auditor"}},
		"resource_access": map[string]any{
			"patient-app": map[string]any{"roles": []string{"doctor"}},
			"another-app": map[string]any{"roles": []string{"administrator"}},
		},
	})

	verified, err := fixture.verifier(t).Verify(context.Background(), fixture.sign(t, payload))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(verified.Roles) != 1 || verified.Roles[0] != "kc:cerbos-poc:patient-app:doctor" {
		t.Fatalf("Roles = %v, want only the configured client's roles", verified.Roles)
	}
}

func TestARealmRoleSourceNormalisesRealmRoles(t *testing.T) {
	fixture := newFixture(t)

	verifier, err := tokenverifier.New(tokenverifier.Config{
		Issuer:     issuer,
		Audience:   audience,
		Realm:      realm,
		RoleSource: tokenverifier.RoleSourceRealm,
		ClientID:   "patient-app",
		Keys:       staticKeys{"test-key": &fixture.key.PublicKey},
		Now:        func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	payload := fixture.valid(claims{
		"realm_access": map[string]any{"roles": []string{"auditor"}},
	})
	verified, err := verifier.Verify(context.Background(), fixture.sign(t, payload))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(verified.Roles) != 1 || verified.Roles[0] != "kc:cerbos-poc:realm:auditor" {
		t.Errorf("Roles = %v, want [kc:cerbos-poc:realm:auditor]", verified.Roles)
	}
}

// Keycloak emits `aud` as a bare string for a single audience and as an array
// once a mapper adds a second one. Both have to verify, or enabling an
// unrelated mapper starts rejecting every token.
func TestAnAudienceArrayContainingTheClientIsAccepted(t *testing.T) {
	fixture := newFixture(t)

	payload := fixture.valid(claims{"aud": []string{"account", audience}})
	if _, err := fixture.verifier(t).Verify(context.Background(), fixture.sign(t, payload)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// --- fixture ---------------------------------------------------------------

type claims map[string]any

type fixture struct {
	key *rsa.PrivateKey
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a signing key: %v", err)
	}
	return &fixture{key: key, now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

// valid is the token Keycloak would mint for the demo doctor. Every rejection
// case starts from it and changes exactly one thing, so a rejection can only be
// attributed to that change.
func (f *fixture) valid(overrides claims) claims {
	payload := claims{
		"iss":                issuer,
		"aud":                audience,
		"sub":                "user-doctor",
		"preferred_username": "doctor",
		"exp":                f.now.Add(time.Hour).Unix(),
		"iat":                f.now.Unix(),
		"tenant_id":          "tenant-a",
		"hospital_id":        "hospital-1",
		"resource_access": map[string]any{
			"patient-app": map[string]any{"roles": []string{"doctor"}},
		},
	}
	for name, value := range overrides {
		payload[name] = value
	}
	return payload
}

func (f *fixture) verifier(t *testing.T) *tokenverifier.Verifier {
	t.Helper()
	verifier, err := tokenverifier.New(tokenverifier.Config{
		Issuer:     issuer,
		Audience:   audience,
		Realm:      realm,
		RoleSource: tokenverifier.RoleSourceClient,
		ClientID:   "patient-app",
		Keys:       staticKeys{"test-key": &f.key.PublicKey},
		Now:        func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return verifier
}

// sign mints a token the way Keycloak would: RS256, a kid in the header, and
// the claims under test.
func (f *fixture) sign(t *testing.T, payload claims) string {
	t.Helper()
	return signWith(t, f.key, "test-key", "RS256", payload)
}

func signWith(t *testing.T, key *rsa.PrivateKey, kid, alg string, payload claims) string {
	t.Helper()
	header := map[string]any{"alg": alg, "typ": "JWT", "kid": kid}
	signing := encodeSegment(t, header) + "." + encodeSegment(t, payload)

	signature, err := signRS256(key, signing)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func encodeSegment(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding a token segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

type staticKeys map[string]*rsa.PublicKey

func (s staticKeys) KeyByID(_ context.Context, kid string) (*rsa.PublicKey, error) {
	key, ok := s[kid]
	if !ok {
		return nil, errors.New("no such key")
	}
	return key, nil
}

func signRS256(key *rsa.PrivateKey, signingInput string) ([]byte, error) {
	digest := sha256.Sum256([]byte(signingInput))
	return rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
}
