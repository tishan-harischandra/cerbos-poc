// Package useroverride implements
// GET/PUT /admin/authz/tenants/{tenant}/hospitals/{hospital}/users/{user}/overrides
// (§9.3, §9.4): the tri-state (INHERIT/GRANT/REVOKE) user-override write path,
// carrying the same transactional and audit guarantees SaveRoleMatrix gives
// the role matrix (§10.1, §16.1).
package useroverride

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/authority"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissionevents"
)

// Store is the narrow slice of assignmentstore.Store this handler needs.
type Store interface {
	// ActiveRolePermissions is read against the roles the request names, to
	// compute the "underlying role result" §9.3 asks the response to report
	// - this is a preview of the pending write, not a live authorization
	// decision, and it computes nothing Cerbos would not compute the same
	// way at request time from the same role_permission rows.
	ActiveRolePermissions(ctx context.Context, query assignmentstore.ActiveRolePermissionQuery) ([]assignmentstore.RolePermission, error)
	ActiveUserOverrides(ctx context.Context, query assignmentstore.ActiveUserOverridesQuery) ([]assignmentstore.UserOverride, error)
	SaveUserOverrideWrite(ctx context.Context, write assignmentstore.UserOverrideWrite) (int64, error)
}

// Catalog validates a resource/action pair against the active catalog
// (§12.2), the same rule rolematrix.Catalog enforces for role grants.
type Catalog interface {
	Has(resource, action string) bool
}

// Effect is the tri-state a PUT request names (§9.3: "Inherit, Grant,
// Revoke"). It is a request-shape concern distinct from
// assignmentstore.OverrideEffect, which has no INHERIT value at all -
// INHERIT is the absence of a row (§8.3), not something the store's own
// vocabulary needs to spell.
type Effect string

const (
	EffectInherit Effect = "INHERIT"
	EffectGrant   Effect = "GRANT"
	EffectRevoke  Effect = "REVOKE"
)

// Handler serves the user-override endpoints.
type Handler struct {
	Store   Store
	Catalog Catalog
	// HighRiskActions names the action keys §9.3's "For high-risk actions,
	// optionally require maker-checker approval" and "Default direct
	// grants to a bounded expiry for high-risk permissions" apply to.
	// There is no catalog-wide risk classification yet (issue #15's scope
	// is the override write path, not the catalog), so this is a small,
	// explicit list rather than a database lookup.
	HighRiskActions []string
	// DefaultHighRiskValidity bounds a high-risk GRANT or REVOKE that
	// names no ValidUntil. Non-positive falls back to
	// DefaultHighRiskValidityWindow.
	DefaultHighRiskValidity time.Duration
	// Now is the clock validity and audit timestamps are measured
	// against. Injected so a test can assert on a fixed value.
	Now func() time.Time
	// NewEventID mints the ids for the audit and outbox rows a save
	// writes. Injected so a test can assert on deterministic ids.
	NewEventID func() string
}

// DefaultHighRiskValidityWindow is the bounded expiry a high-risk GRANT or
// REVOKE defaults to when the request names none (§9.3: "the safe choice is
// the default choice").
const DefaultHighRiskValidityWindow = 90 * 24 * time.Hour

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

func (h *Handler) isHighRisk(action string) bool {
	for _, risky := range h.HighRiskActions {
		if risky == action {
			return true
		}
	}
	return false
}

func (h *Handler) defaultHighRiskValidity() time.Duration {
	if h.DefaultHighRiskValidity > 0 {
		return h.DefaultHighRiskValidity
	}
	return DefaultHighRiskValidityWindow
}

type saveRequest struct {
	ExpectedRevision int64      `json:"expectedRevision"`
	ResourceKey      string     `json:"resourceKey"`
	ActionKey        string     `json:"actionKey"`
	Effect           Effect     `json:"effect"`
	Reason           string     `json:"reason"`
	ValidFrom        time.Time  `json:"validFrom"`
	ValidUntil       *time.Time `json:"validUntil,omitempty"`
	// RoleExternalIDs are the canonical roles currently assigned to the
	// target user, as the caller already knows them (§7.5). This handler
	// never resolves a user's roles itself - that is the identity
	// directory's job (issue #24's normalized IdP reads) - it only
	// previews the combination the caller supplies.
	RoleExternalIDs []string `json:"roleExternalIds"`
}

// Save handles PUT .../overrides.
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	tenant := r.PathValue("tenant")
	hospital := r.PathValue("hospital")
	user := r.PathValue("user")

	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		tenant, hospital); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant and hospital")
		return
	}

	var req saveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	if req.ResourceKey == "" || req.ActionKey == "" {
		writeError(w, http.StatusBadRequest, "resourceKey and actionKey are required")
		return
	}
	if !h.Catalog.Has(req.ResourceKey, req.ActionKey) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("not in the active catalog: %s:%s", req.ResourceKey, req.ActionKey))
		return
	}

	var storeEffect assignmentstore.OverrideEffect
	switch req.Effect {
	case EffectInherit:
		storeEffect = ""
	case EffectGrant:
		storeEffect = assignmentstore.EffectGrant
	case EffectRevoke:
		storeEffect = assignmentstore.EffectRevoke
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("effect must be INHERIT, GRANT or REVOKE, got %q", req.Effect))
		return
	}

	if storeEffect != "" {
		// §9.3: "Require ... reason, validity start and optional expiry."
		// INHERIT clears a row rather than creating one, so it needs
		// neither.
		if req.Reason == "" {
			writeError(w, http.StatusBadRequest, "reason is required for a GRANT or REVOKE")
			return
		}
		if req.ValidFrom.IsZero() {
			writeError(w, http.StatusBadRequest, "validFrom is required for a GRANT or REVOKE")
			return
		}
	}

	now := h.now()
	var validUntil time.Time
	if req.ValidUntil != nil {
		validUntil = *req.ValidUntil
	} else if storeEffect != "" && h.isHighRisk(req.ActionKey) {
		// §9.3: "Default direct grants to a bounded expiry for high-risk
		// permissions" - the safe choice is the default choice, so an
		// administrator who names no expiry for a high-risk action gets
		// one anyway rather than an unbounded grant.
		validUntil = req.ValidFrom.Add(h.defaultHighRiskValidity())
	}

	roleResult := h.roleResult(r.Context(), tenant, req.ResourceKey, req.ActionKey, req.RoleExternalIDs, now)
	effectiveResult := effectiveResult(storeEffect, roleResult)

	correlationID := r.Header.Get("X-Correlation-Id")
	if correlationID == "" {
		correlationID = h.newEventID()
	}

	event := permissionevents.PermissionChanged{
		EventID:     h.newEventID(),
		EventType:   permissionevents.EventTypePermissionChanged,
		TenantID:    tenant,
		HospitalID:  hospital,
		SubjectType: permissionevents.SubjectUser,
		SubjectID:   user,
		Resource:    req.ResourceKey,
		Action:      req.ActionKey,
		Enabled:     effectiveResult,
		Revision:    req.ExpectedRevision + 1,
		OccurredAt:  now,
	}
	payload, _ := json.Marshal([]permissionevents.PermissionChanged{event})

	write := assignmentstore.UserOverrideWrite{
		Key: assignmentstore.UserOverrideKey{
			TenantID: tenant, HospitalID: hospital, UserExternalID: user,
			ResourceKey: req.ResourceKey, ActionKey: req.ActionKey,
		},
		Effect:           storeEffect,
		Reason:           req.Reason,
		ValidFrom:        req.ValidFrom,
		ValidUntil:       validUntil,
		ExpectedRevision: req.ExpectedRevision,
		Audit: assignmentstore.AuditEvent{
			EventID:       h.newEventID(),
			ActorID:       identity.PrincipalID,
			Operation:     "USER_OVERRIDE_SAVE",
			TargetType:    "user_permission_override",
			BeforeJSON:    fmt.Sprintf(`{"roleResult":%t}`, roleResult),
			AfterJSON:     fmt.Sprintf(`{"effect":%q,"effectiveResult":%t}`, req.Effect, effectiveResult),
			CorrelationID: correlationID,
			CreatedAt:     now,
		},
		Outbox: assignmentstore.OutboxEvent{
			EventID:      h.newEventID(),
			AggregateKey: tenant + ":" + user,
			EventType:    permissionevents.EventTypePermissionChanged,
			Payload:      string(payload),
			CreatedAt:    now,
		},
	}

	newRevision, err := h.Store.SaveUserOverrideWrite(r.Context(), write)
	if errors.Is(err, assignmentstore.ErrRevisionConflict) {
		writeError(w, http.StatusConflict, "expected revision is stale; reload the current overrides and retry")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "saving the user override failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"revision":          newRevision,
		"roleResult":        roleResult,
		"effectiveResult":   effectiveResult,
		"noPracticalEffect": effectiveResult == roleResult,
	})
}

// roleResult reports whether any of roleExternalIDs currently grants
// resource/action, the same read ActiveRolePermissions gives the ADS's own
// decision path (§11.2) - a preview computed from the same source of truth,
// not a second implementation of one.
func (h *Handler) roleResult(ctx context.Context, tenant, resource, action string, roleExternalIDs []string, at time.Time) bool {
	if len(roleExternalIDs) == 0 {
		return false
	}
	permissions, err := h.Store.ActiveRolePermissions(ctx, assignmentstore.ActiveRolePermissionQuery{
		TenantID: tenant, RoleExternalIDs: roleExternalIDs, ResourceKey: resource, At: at,
	})
	if err != nil {
		return false
	}
	for _, permission := range permissions {
		if permission.Key.ActionKey == action && permission.Enabled {
			return true
		}
	}
	return false
}

// effectiveResult applies the tri-state override to a role result: REVOKE
// always defeats it, GRANT always wins over it, and INHERIT leaves it
// unmodified. This mirrors the §6.3 "user-specific outranks role-specific"
// rule for the two dimensions this preview covers - it omits the mandatory
// rule that outranks both, because that rule is Cerbos policy's alone to
// evaluate (§6.3, §6.4) and this is an administrative preview of a pending
// write, not a decision.
func effectiveResult(effect assignmentstore.OverrideEffect, roleResult bool) bool {
	switch effect {
	case assignmentstore.EffectRevoke:
		return false
	case assignmentstore.EffectGrant:
		return true
	default:
		return roleResult
	}
}

// overrideView is one row of a GET .../overrides response.
type overrideView struct {
	ActionKey  string     `json:"actionKey"`
	Effect     string     `json:"effect"`
	Enabled    bool       `json:"enabled"`
	Reason     string     `json:"reason,omitempty"`
	ValidFrom  time.Time  `json:"validFrom"`
	ValidUntil *time.Time `json:"validUntil,omitempty"`
	Revision   int64      `json:"revision"`
}

// Read handles GET .../overrides?resource=....
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	tenant := r.PathValue("tenant")
	hospital := r.PathValue("hospital")
	user := r.PathValue("user")

	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		tenant, hospital); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant and hospital")
		return
	}

	resource := r.URL.Query().Get("resource")
	if resource == "" {
		writeError(w, http.StatusBadRequest, "a resource query parameter is required")
		return
	}

	overrides, err := h.Store.ActiveUserOverrides(r.Context(), assignmentstore.ActiveUserOverridesQuery{
		TenantID: tenant, HospitalID: hospital, UserExternalID: user,
		ResourceKey: resource, At: h.now(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading the user overrides failed")
		return
	}

	views := make([]overrideView, 0, len(overrides))
	for _, override := range overrides {
		view := overrideView{
			ActionKey: override.Key.ActionKey,
			Effect:    string(override.Effect),
			Enabled:   override.Enabled,
			Reason:    override.Reason,
			ValidFrom: override.ValidFrom,
			Revision:  override.Revision,
		}
		if !override.ValidUntil.IsZero() {
			validUntil := override.ValidUntil
			view.ValidUntil = &validUntil
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"overrides": views})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
