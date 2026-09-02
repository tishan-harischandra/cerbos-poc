// Package tenantonboarding implements POST /admin/authz/tenants (issue
// #86): bringing up a new hospital group at runtime, with no release and
// no service restart.
//
// The registry is file-seeded but database-of-record (issue #76): this
// handler writes the same tenant_registry row a file-seeded entry would,
// validated against the identical rules (libs/tenantregistry.Validate),
// so there is exactly one definition of "a valid tenant" rather than two
// that could drift apart. libs/assignmentstore/tenantseed no longer
// overwrites a realm that already has a row, so a tenant onboarded here
// survives the next `make up`'s re-seed unchanged.
//
// This endpoint is deliberately not scoped to the caller's own tenant the
// way the role-matrix and user-override writes are (§9.4's authority
// check): the tenant being onboarded does not exist yet, so there is no
// existing tenant to validate authority against. Any authenticated
// administrator - from any already-registered realm - may onboard a new
// one. This is consistent with the rest of this prototype's
// administration surface, which trusts an authenticated caller rather
// than checking a specific role (Cerbos policy, not this package, is
// where role-based business authorization lives); it is not a narrower
// "platform operator" credential this codebase has anywhere else to
// reuse. See docs/MEASURED_FINDINGS.md for the tradeoff this leaves open.
package tenantonboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

// Store is the narrow slice of assignmentstore.Store this handler needs.
type Store interface {
	Tenant(ctx context.Context, realm string) (assignmentstore.Tenant, bool, error)
	SaveTenant(ctx context.Context, tenant assignmentstore.Tenant) error
	AppendAuditEvent(ctx context.Context, event assignmentstore.AuditEvent) error
}

type onboardRequest struct {
	Realm               string `json:"realm"`
	Issuer              string `json:"issuer"`
	BrowserClientID     string `json:"browserClientId"`
	ServiceClientID     string `json:"serviceClientId"`
	CredentialSecretRef string `json:"credentialSecretRef"`
}

// Handler serves the tenant onboarding endpoint.
type Handler struct {
	Store Store
	// Now is the clock the audit row is stamped with. Injected so a test
	// can assert on a fixed value.
	Now func() time.Time
	// NewEventID mints the audit row's id. Injected so a test can assert
	// on a deterministic id.
	NewEventID func() string
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) newEventID() string {
	if h.NewEventID != nil {
		return h.NewEventID()
	}
	return fmt.Sprintf("evt-%d", time.Now().UnixNano())
}

// Onboard handles POST /admin/authz/tenants.
func (h *Handler) Onboard(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	var req onboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	serviceClientID := req.ServiceClientID
	if serviceClientID == "" {
		serviceClientID = req.BrowserClientID
	}
	entry := tenantregistry.Entry{
		Realm:               req.Realm,
		Issuer:              req.Issuer,
		BrowserClientID:     req.BrowserClientID,
		ServiceClientID:     serviceClientID,
		CredentialSecretRef: req.CredentialSecretRef,
	}
	// The same rule the registry file's rows are validated against
	// (issue #86's acceptance criterion): one definition, not two.
	if err := tenantregistry.Validate(entry); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, exists, err := h.Store.Tenant(r.Context(), entry.Realm); err != nil {
		writeError(w, http.StatusInternalServerError, "reading the tenant registry failed")
		return
	} else if exists {
		writeError(w, http.StatusConflict, fmt.Sprintf("realm %q is already registered", entry.Realm))
		return
	}

	if err := h.Store.SaveTenant(r.Context(), assignmentstore.Tenant{
		Realm:               entry.Realm,
		Issuer:              entry.Issuer,
		BrowserClientID:     entry.BrowserClientID,
		ServiceClientID:     entry.ServiceClientID,
		CredentialSecretRef: entry.CredentialSecretRef,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "saving the tenant registry row failed")
		return
	}

	// Audited like every other administrative write (issue #86's
	// acceptance criterion), even though there is no permission matrix or
	// override to bundle it with atomically: onboarding writes exactly
	// one row, and the audit trail records that it happened, by whom, and
	// with what before/after state.
	afterJSON, _ := json.Marshal(entry)
	correlationID := r.Header.Get("X-Correlation-Id")
	if correlationID == "" {
		correlationID = h.newEventID()
	}
	if err := h.Store.AppendAuditEvent(r.Context(), assignmentstore.AuditEvent{
		EventID:       h.newEventID(),
		ActorID:       identity.PrincipalID,
		Operation:     "TENANT_ONBOARD",
		TargetType:    "tenant_registry",
		BeforeJSON:    "{}",
		AfterJSON:     string(afterJSON),
		TenantID:      entry.Realm,
		CorrelationID: correlationID,
		CreatedAt:     h.now(),
	}); err != nil {
		// The tenant is already saved and usable; a failure to audit it
		// is an observability gap to log, not a reason to tell the caller
		// the onboarding itself failed.
		writeError(w, http.StatusInternalServerError, "the tenant was onboarded, but recording the audit event failed")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"realm": entry.Realm})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
