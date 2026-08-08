// Package platformstatus implements the Admin Console's revision and
// activation module plus IdP diagnostics (issue #22, §9.1, §17.1):
// current permission revision and cache convergence per tenant, the
// current root policy revision and release history, and the selected
// identity provider's connectivity and role/token mapping configuration.
package platformstatus

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/authority"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

// idPHealthCheckTimeout bounds the live connectivity check IdPDiagnostics
// makes against the ADS's identity directory proxy. Without a deadline of
// its own, an IdP outage - the exact condition this check exists to
// report - would hang the request for as long as the outbound connection
// takes to fail at the TCP level, which can far exceed any caller's
// timeout instead of reporting "degraded" promptly.
const idPHealthCheckTimeout = 3 * time.Second

// PolicyStore is the slice of policyrelease.Store this handler reads.
type PolicyStore interface {
	Active() (policyrelease.Archive, error)
	History() ([]policyrelease.HistoryEntry, error)
}

// ADS is the slice of the ADS's status surface this handler proxies.
type ADS interface {
	Convergence(ctx context.Context, token string) (adsclient.ConvergenceResponse, error)
	// DirectoryHealth reports whether the ADS could reach the identity
	// provider's admin API just now, from a request the diagnostics module
	// makes for exactly that purpose - never from the decision path, which
	// never depends on the IdP being up.
	DirectoryHealth(ctx context.Context, token string) error
}

// Handler serves the platform status endpoints.
type Handler struct {
	PolicyStore PolicyStore
	ADS         ADS

	// IdPType, IdPRoleSource and IdPTenantMappingMode are the installation's
	// §7.1 identity configuration, the same one the token verifier and
	// directory adapter were built from - reported here as installation
	// facts, never as a live credential (§16.1).
	IdPType              string
	IdPRoleSource        string
	IdPTenantMappingMode string
}

type releasePayload struct {
	Revision string `json:"revision"`
	Commit   string `json:"commit"`
	SHA256   string `json:"sha256"`
}

type historyPayload struct {
	Revision   string `json:"revision"`
	Commit     string `json:"commit"`
	Activated  bool   `json:"activated"`
	Error      string `json:"error,omitempty"`
	RecordedAt string `json:"recordedAt"`
}

// PolicyReleases handles GET /admin/authz/policy-releases: the current root
// policy revision and every recorded release attempt, oldest first.
func (h *Handler) PolicyReleases(w http.ResponseWriter, r *http.Request) {
	if _, ok := tokenauth.From(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	var current *releasePayload
	if active, err := h.PolicyStore.Active(); err == nil {
		current = &releasePayload{Revision: active.Revision, Commit: active.Commit, SHA256: active.SHA256}
	}

	entries, err := h.PolicyStore.History()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading the release history failed")
		return
	}
	history := make([]historyPayload, 0, len(entries))
	for _, e := range entries {
		history = append(history, historyPayload{
			Revision: e.Revision, Commit: e.Commit, Activated: e.Activated,
			Error: e.Error, RecordedAt: e.RecordedAt.Format(timeFormat),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"current": current, "history": history})
}

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

// Convergence handles GET /admin/authz/tenants/{tenant}/convergence,
// proxying the ADS's own cache convergence report for that tenant.
func (h *Handler) Convergence(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}
	tenant := r.PathValue("tenant")
	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		tenant, ""); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant")
		return
	}

	resp, err := h.ADS.Convergence(r.Context(), identity.RawToken)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the assignment data service could not be reached")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// IdPDiagnostics handles GET /admin/idp/diagnostics: the selected provider,
// its role and token mapping configuration, and a live connectivity check
// against its admin API (issue #22's "degraded search while runtime
// authorization continues unaffected").
func (h *Handler) IdPDiagnostics(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), idPHealthCheckTimeout)
	defer cancel()

	connectivity := "ok"
	if err := h.ADS.DirectoryHealth(ctx, identity.RawToken); err != nil {
		connectivity = "degraded"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider":          h.IdPType,
		"roleSource":        h.IdPRoleSource,
		"tenantMappingMode": h.IdPTenantMappingMode,
		"connectivity":      connectivity,
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
