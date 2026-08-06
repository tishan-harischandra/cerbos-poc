// Package tokenauth turns a bearer token into the identity a handler may act
// on.
//
// It is the only place the Administration Service decides who a caller is.
// Handlers read the identity from the request context, never from a request
// body: §16.1 requires tenant and hospital context to be derived server-side,
// and an administrator's own tenant/hospital scope is exactly that kind of
// context.
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
	// Roles are canonical §7.5 identifiers.
	Roles []string
	// RawToken is the verified bearer token exactly as it arrived, so the
	// simulator (issue #19) can present the same credential to the ADS's
	// internal simulate endpoints rather than minting one of its own.
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
