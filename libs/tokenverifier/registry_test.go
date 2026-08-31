package tokenverifier_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// A registry routes each token to the realm named by its own issuer, and
// that realm's own audience and keys decide it - no service concatenates an
// issuer or a key-set URL by hand to do this (issue #77).
func TestARegistryVerifiesEachTokenAgainstTheRealmItsIssuerNames(t *testing.T) {
	a := newRealmFixture(t, "http://keycloak:8080/realms/tenant-a", "tenant-a")
	b := newRealmFixture(t, "http://keycloak:8080/realms/tenant-b", "tenant-b")

	registry := tokenverifier.NewRegistry()
	registry.Register(a.issuer, a.verifier(t))
	registry.Register(b.issuer, b.verifier(t))

	verifiedA, err := registry.Verify(context.Background(), a.sign(t, a.valid(nil)))
	if err != nil {
		t.Fatalf("verifying tenant a's token: %v", err)
	}
	if verifiedA.TenantID != "tenant-a" {
		t.Errorf("tenant a's token resolved to tenant %q, want tenant-a", verifiedA.TenantID)
	}

	verifiedB, err := registry.Verify(context.Background(), b.sign(t, b.valid(nil)))
	if err != nil {
		t.Fatalf("verifying tenant b's token: %v", err)
	}
	if verifiedB.TenantID != "tenant-b" {
		t.Errorf("tenant b's token resolved to tenant %q, want tenant-b", verifiedB.TenantID)
	}
}

// The isolation the whole registry exists for: a token that verifies
// perfectly against its own realm must still be refused by every other
// realm's keys, because it was never signed by them.
func TestARegistryRefusesATokenSignedByAKeyItsIssuerDoesNotOwn(t *testing.T) {
	a := newRealmFixture(t, "http://keycloak:8080/realms/tenant-a", "tenant-a")
	b := newRealmFixture(t, "http://keycloak:8080/realms/tenant-b", "tenant-b")

	registry := tokenverifier.NewRegistry()
	registry.Register(a.issuer, a.verifier(t))
	registry.Register(b.issuer, b.verifier(t))

	// A token claiming tenant b's issuer but signed by tenant a's key: the
	// registry picks tenant b's Verifier from the issuer, which then rejects
	// the signature because it was never made by a key tenant b publishes.
	forged := signWith(t, a.key, "test-key", "RS256", b.valid(claims{"iss": b.issuer}))
	if _, err := registry.Verify(context.Background(), forged); !errors.Is(err, tokenverifier.ErrUnknownKey) &&
		!errors.Is(err, tokenverifier.ErrInvalidSignature) {
		t.Errorf("Verify error = %v, want ErrUnknownKey or ErrInvalidSignature", err)
	}
}

// An issuer no realm was registered for is refused outright, the same as any
// other unrecognised issuer.
func TestARegistryRefusesAnUnregisteredIssuer(t *testing.T) {
	registry := tokenverifier.NewRegistry()
	a := newRealmFixture(t, "http://keycloak:8080/realms/tenant-a", "tenant-a")

	_, err := registry.Verify(context.Background(), a.sign(t, a.valid(nil)))
	if !errors.Is(err, tokenverifier.ErrInvalidIssuer) {
		t.Errorf("Verify error = %v, want ErrInvalidIssuer", err)
	}
}

// Signing keys are cached per realm (issue #77): forcing tenant a's cached
// key set empty - the shape a rotation leaves behind until the next fetch -
// must never disturb tenant b's independently cached keys.
func TestAKeyRotationInOneRealmDoesNotDisturbAnother(t *testing.T) {
	a := newRealmFixture(t, "http://keycloak:8080/realms/tenant-a", "tenant-a")
	b := newRealmFixture(t, "http://keycloak:8080/realms/tenant-b", "tenant-b")

	registry := tokenverifier.NewRegistry()
	aKeys := staticKeys{"test-key": &a.key.PublicKey}
	registry.Register(a.issuer, mustVerifier(t, a, aKeys))
	registry.Register(b.issuer, mustVerifier(t, b, staticKeys{"test-key": &b.key.PublicKey}))

	// tenant a rotates away its only published key.
	delete(aKeys, "test-key")

	if _, err := registry.Verify(context.Background(), a.sign(t, a.valid(nil))); !errors.Is(err, tokenverifier.ErrUnknownKey) {
		t.Fatalf("tenant a after rotation: err = %v, want ErrUnknownKey", err)
	}

	// tenant b's own key was never touched.
	verifiedB, err := registry.Verify(context.Background(), b.sign(t, b.valid(nil)))
	if err != nil {
		t.Fatalf("tenant b after tenant a's rotation: %v", err)
	}
	if verifiedB.TenantID != "tenant-b" {
		t.Errorf("tenant b's token resolved to tenant %q, want tenant-b", verifiedB.TenantID)
	}
}

// --- fixture -----------------------------------------------------------

type realmFixture struct {
	issuer string
	realm  string
	key    *rsa.PrivateKey
	now    time.Time
}

func newRealmFixture(t *testing.T, issuer, realm string) *realmFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a signing key: %v", err)
	}
	return &realmFixture{
		issuer: issuer,
		realm:  realm,
		key:    key,
		now:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func (f *realmFixture) valid(overrides claims) claims {
	payload := claims{
		"iss":                f.issuer,
		"aud":                audience,
		"sub":                "user-" + f.realm,
		"preferred_username": f.realm + "-user",
		"exp":                f.now.Add(time.Hour).Unix(),
		"iat":                f.now.Unix(),
		"resource_access": map[string]any{
			"patient-app": map[string]any{"roles": []string{"doctor"}},
		},
	}
	for name, value := range overrides {
		payload[name] = value
	}
	return payload
}

func (f *realmFixture) verifier(t *testing.T) *tokenverifier.Verifier {
	t.Helper()
	return mustVerifier(t, f, staticKeys{"test-key": &f.key.PublicKey})
}

func mustVerifier(t *testing.T, f *realmFixture, keys tokenverifier.KeySource) *tokenverifier.Verifier {
	t.Helper()
	verifier, err := tokenverifier.New(tokenverifier.Config{
		Issuer:     f.issuer,
		Audience:   audience,
		Realm:      f.realm,
		RoleSource: tokenverifier.RoleSourceClient,
		ClientID:   "patient-app",
		Keys:       keys,
		Now:        func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return verifier
}

func (f *realmFixture) sign(t *testing.T, payload claims) string {
	t.Helper()
	return signWith(t, f.key, "test-key", "RS256", payload)
}
