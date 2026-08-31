package tokenauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// The identity a handler acts on comes from the token and from nothing else.
func TestAVerifiedTokenPutsTheIdentityInTheRequestContext(t *testing.T) {
	var seen tokenauth.Identity
	handler := tokenauth.Require(tokenauth.Config{Verifier: verifier{token: tokenverifier.VerifiedToken{
		Subject:    "user-doctor",
		TenantID:   "tenant-a",
		HospitalID: "hospital-1",
		Roles:      []string{"kc:tenant-a:patient-app:doctor"},
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
	if len(seen.Roles) != 1 || seen.Roles[0] != "kc:tenant-a:patient-app:doctor" {
		t.Errorf("Roles = %v, want the token's canonical roles", seen.Roles)
	}
}

// The resource service forwards the caller's own token to the ADS's decision
// endpoint rather than minting one of its own, so the identity it builds must
// carry the raw token unchanged.
func TestTheIdentityCarriesTheRawTokenForForwardingToTheADS(t *testing.T) {
	var seen tokenauth.Identity
	handler := tokenauth.Require(
		tokenauth.Config{Verifier: verifier{token: tokenverifier.VerifiedToken{Subject: "user-doctor"}}},
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			identity, _ := tokenauth.From(r.Context())
			seen = identity
		}))

	handler.ServeHTTP(httptest.NewRecorder(), authorized("a-token"))

	if seen.RawToken != "a-token" {
		t.Errorf("RawToken = %q, want %q", seen.RawToken, "a-token")
	}
}

func TestARequestWithNoBearerTokenIsUnauthorized(t *testing.T) {
	requests := map[string]*http.Request{
		"no Authorization header": httptest.NewRequest(http.MethodGet, "/fhir/patient_record/patient-1", nil),
		"a scheme nobody agreed to": withHeader(
			httptest.NewRequest(http.MethodGet, "/fhir/patient_record/patient-1", nil),
			"Basic dXNlcjpwYXNz"),
		"an empty bearer token": withHeader(
			httptest.NewRequest(http.MethodGet, "/fhir/patient_record/patient-1", nil), "Bearer "),
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
	return withHeader(httptest.NewRequest(http.MethodGet, "/fhir/patient_record/patient-1", nil), "Bearer "+token)
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
