// Package tokenauth turns a bearer token into the identity a handler may act
// on.
//
// This mirrors apps/ads/internal/tokenauth deliberately rather than sharing
// it: the two are separate Go modules, and each service's HTTP surface is
// its own composition root. The one thing that must never differ is the
// underlying verification in libs/tokenverifier, which both wrap identically
// (§16.1: tenant and hospital context are derived server-side).
package tokenauth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// Identity is who the caller is, as the identity provider attested.
type Identity struct {
	PrincipalID string
	Username    string
	TenantID    string
	HospitalID  string
	Roles       []string
	// RawToken is the verified bearer token exactly as it arrived, so a
	// downstream call to another internal service (the ADS's decision
	// endpoint) can present the same credential rather than one this
	// service minted itself.
	RawToken string
}

// Verifier is the token verification this middleware delegates to.
type Verifier interface {
	Verify(ctx context.Context, raw string) (tokenverifier.VerifiedToken, error)
}

// Config holds the middleware's collaborators.
type Config struct {
	Verifier Verifier
	Logger   *slog.Logger
}

type contextKey struct{}

// From returns the identity established for this request, if any.
func From(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(contextKey{}).(Identity)
	return identity, ok
}

// WithIdentity puts an identity in a context. It exists for tests and for
// callers that have already verified a token; the middleware is the only
// production path.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, identity)
}

const bearerPrefix = "Bearer "

// Require verifies the caller's bearer token and passes the resulting
// identity to next. A request that fails verification never reaches next.
func Require(cfg Config, next http.Handler) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "a bearer token is required")
			return
		}

		verified, err := cfg.Verifier.Verify(r.Context(), raw)
		if err != nil {
			if errors.Is(err, tokenverifier.ErrReservedRole) {
				logger.WarnContext(r.Context(), "rejected a token presenting a reserved role",
					slog.Any("error", err))
				writeError(w, http.StatusForbidden, "the token carries a role reserved for the platform")
				return
			}
			// An unscoped token (issue #78) is a distinct, logged reason:
			// it is not a forgery attempt, but it is not an ordinary
			// expired-or-malformed rejection either.
			if errors.Is(err, tokenverifier.ErrUnscopedToken) || errors.Is(err, tokenverifier.ErrAmbiguousOrganization) {
				logger.WarnContext(r.Context(), "rejected an unscoped token", slog.Any("error", err))
				writeError(w, http.StatusUnauthorized, "the token names no usable hospital scope")
				return
			}
			logger.WarnContext(r.Context(), "rejected a bearer token", slog.Any("error", err))
			writeError(w, http.StatusUnauthorized, "the bearer token was refused")
			return
		}

		identity := Identity{
			PrincipalID: verified.Subject,
			Username:    verified.Username,
			TenantID:    verified.TenantID,
			HospitalID:  verified.HospitalID,
			Roles:       verified.Roles,
			RawToken:    raw,
		}
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
	})
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearerPrefix):])
	return token, token != ""
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
