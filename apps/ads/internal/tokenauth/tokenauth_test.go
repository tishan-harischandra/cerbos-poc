package tokenauth_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// The identity a handler acts on comes from the token and from nothing else.
func TestAVerifiedTokenPutsTheIdentityInTheRequestContext(t *testing.T) {
	var seen tokenauth.Identity
	handler := tokenauth.Require(tokenauth.Config{Verifier: verifier{token: tokenverifier.VerifiedToken{
		Subject:    "user-doctor",
		TenantID:   "tenant-a",
		HospitalID: "hospital-1",
		Roles:      []string{"kc:tenant-a:realm:doctor"},
	}}}, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		identity, ok := tokenauth.From(r.Context())
		if !ok {
			t.Error("the handler saw no identity")
		}
		seen = identity
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authorized("a-token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if seen.PrincipalID != "user-doctor" {
		t.Errorf("PrincipalID = %q, want %q", seen.PrincipalID, "user-doctor")
	}
	if seen.TenantID != "tenant-a" || seen.HospitalID != "hospital-1" {
		t.Errorf("tenant/hospital = %q/%q, want tenant-a/hospital-1", seen.TenantID, seen.HospitalID)
	}
	if len(seen.Roles) != 1 || seen.Roles[0] != "kc:tenant-a:realm:doctor" {
		t.Errorf("Roles = %v, want the token's canonical roles", seen.Roles)
	}
}

func TestARequestWithNoBearerTokenIsUnauthorized(t *testing.T) {
	requests := map[string]*http.Request{
		"no Authorization header": httptest.NewRequest(http.MethodPost, "/internal/authz/check", nil),
		"a scheme nobody agreed to": withHeader(
			httptest.NewRequest(http.MethodPost, "/internal/authz/check", nil),
			"Basic dXNlcjpwYXNz"),
		"an empty bearer token": withHeader(
			httptest.NewRequest(http.MethodPost, "/internal/authz/check", nil), "Bearer "),
	}

	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			handler := tokenauth.Require(
				tokenauth.Config{Verifier: verifier{token: tokenverifier.VerifiedToken{Subject: "user-doctor"}}},
				refuseToRun(t))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, request)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestARejectedTokenNeverReachesTheHandler(t *testing.T) {
	handler := tokenauth.Require(
		tokenauth.Config{Verifier: verifier{err: tokenverifier.ErrExpired}},
		refuseToRun(t))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authorized("an-expired-token"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	// The caller learns that the token was refused, not which check refused
	// it: the detail belongs in the service log, not in a probe's response.
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if strings.Contains(strings.ToLower(body["error"]), "expired") {
		t.Errorf("the response told the caller which check failed: %q", body["error"])
	}
}

// §16.1: a token claiming the synthetic role is a caller trying to be the
// platform. It is refused, and the refusal is distinguishable from a merely
// invalid token so it can be alerted on.
func TestATokenClaimingTheSyntheticRoleIsForbiddenRatherThanUnauthorized(t *testing.T) {
	handler := tokenauth.Require(
		tokenauth.Config{Verifier: verifier{err: tokenverifier.ErrReservedRole}},
		refuseToRun(t))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authorized("a-forged-token"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// §78: an unscoped token is refused distinguishably, logged separately from
// an ordinary rejection, even though the caller still only learns that the
// token was refused.
func TestAnUnscopedTokenIsLoggedDistinguishably(t *testing.T) {
	for _, err := range []error{tokenverifier.ErrUnscopedToken, tokenverifier.ErrAmbiguousOrganization} {
		t.Run(err.Error(), func(t *testing.T) {
			var logged strings.Builder
			handler := tokenauth.Require(
				tokenauth.Config{
					Verifier: verifier{err: err},
					Logger:   slog.New(slog.NewJSONHandler(&logged, nil)),
				},
				refuseToRun(t))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, authorized("an-unscoped-token"))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if !strings.Contains(logged.String(), "unscoped") {
				t.Errorf("no log record named an unscoped token; log was:\n%s", logged.String())
			}
		})
	}
}

func TestAHandlerReadingTheContextWithoutMiddlewareGetsNoIdentity(t *testing.T) {
	if _, ok := tokenauth.From(context.Background()); ok {
		t.Error("an identity appeared in a context nothing authenticated")
	}
}

type verifier struct {
	token tokenverifier.VerifiedToken
	err   error
}

func (v verifier) Verify(context.Context, string) (tokenverifier.VerifiedToken, error) {
	return v.token, v.err
}

func authorized(token string) *http.Request {
	return withHeader(httptest.NewRequest(http.MethodPost, "/internal/authz/check", nil), "Bearer "+token)
}

func withHeader(r *http.Request, authorization string) *http.Request {
	r.Header.Set("Authorization", authorization)
	return r
}

func refuseToRun(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the handler ran for a request that should never have been authenticated")
	})
}
