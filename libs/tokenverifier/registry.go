package tokenverifier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Registry verifies tokens across every realm an installation trusts (issue
// #77): the issuer the token itself claims selects which realm's Verifier
// checks it, and that Verifier supplies its own issuer, audience and key
// source. No service concatenates an issuer or a key-set URL by hand, and a
// key rotation in one realm never touches another realm's cached keys,
// because each registered Verifier owns its own KeySource.
//
// The issuer read here to pick a Verifier is unverified input - exactly the
// same status any other header of an unauthenticated request has. It is
// never trusted on its own: the Verifier it selects performs the full §16.1
// signature check, including its own issuer comparison, so a forged issuer
// picks the wrong Verifier and then fails signature verification against
// that realm's keys.
type Registry struct {
	mu       sync.RWMutex
	byIssuer map[string]*Verifier
}

// NewRegistry returns an empty multi-issuer verifier.
func NewRegistry() *Registry {
	return &Registry{byIssuer: make(map[string]*Verifier)}
}

// Register adds a realm's Verifier under its issuer. Registering the same
// issuer twice replaces the previous Verifier, so a realm can be re-keyed
// without restarting the process.
func (r *Registry) Register(issuer string, verifier *Verifier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byIssuer[issuer] = verifier
}

// Verify peeks the token's unverified issuer to select a realm, then
// performs the full identity check with that realm's own Verifier.
func (r *Registry) Verify(ctx context.Context, raw string) (VerifiedToken, error) {
	issuer, err := peekIssuer(raw)
	if err != nil {
		return VerifiedToken{}, err
	}

	r.mu.RLock()
	verifier, ok := r.byIssuer[issuer]
	r.mu.RUnlock()
	if !ok {
		return VerifiedToken{}, fmt.Errorf("%w: %q", ErrInvalidIssuer, issuer)
	}
	return verifier.Verify(ctx, raw)
}

// peekIssuer reads the `iss` claim without verifying the token's signature.
// It exists only to pick which realm's Verifier performs the real check;
// nothing here is trusted as an identity fact.
func peekIssuer(raw string) (string, error) {
	segments := strings.Split(strings.TrimSpace(raw), ".")
	if len(segments) != 3 {
		return "", ErrMalformedToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return "", ErrMalformedToken
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return "", ErrMalformedToken
	}
	return claims.Issuer, nil
}
