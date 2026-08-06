// Package rolematrix implements PUT /admin/authz/tenants/{tenant}/roles/{role}/permissions
// (§9.4): replacing a role's permission slice with an expected-revision
// precondition, backed by assignmentstore.Store.SaveRoleMatrix's atomic
// write of the permission, audit event, outbox event and revision bump
// (§10.1, §16.1).
package rolematrix

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
	RolePermission(ctx context.Context, key assignmentstore.RolePermissionKey) (assignmentstore.RolePermission, bool, error)
	RolePermissionsForRole(ctx context.Context, tenantID, roleExternalID string) ([]assignmentstore.RolePermission, error)
	SaveRoleMatrix(ctx context.Context, write assignmentstore.RoleMatrixWrite) (int64, error)
	PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error)
}

// Catalog validates a resource/action pair against the active catalog
// (§12.2: "Every permission leaf must resolve to a known catalog resource
// and action" - the same rule applies to what an administrator may grant).
type Catalog interface {
	Has(resource, action string) bool
}

// PermissionInput is one row of the role-matrix slice a PUT request carries.
type PermissionInput struct {
	ResourceKey string     `json:"resourceKey"`
	ActionKey   string     `json:"actionKey"`
	Enabled     bool       `json:"enabled"`
	ValidFrom   time.Time  `json:"validFrom"`
	ValidUntil  *time.Time `json:"validUntil,omitempty"`
}

type saveRequest struct {
	ExpectedRevision int64             `json:"expectedRevision"`
	Permissions      []PermissionInput `json:"permissions"`
}

// Handler serves the role-matrix endpoints.
type Handler struct {
	Store   Store
	Catalog Catalog
	// Now is the clock audit and outbox rows are stamped with. Injected so
	// a test can assert on a fixed value.
	Now func() time.Time
	// NewEventID mints the ids for the audit and outbox rows a save writes.
	// Injected so a test can assert on deterministic ids.
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

// Save handles PUT /admin/authz/tenants/{tenant}/roles/{role}/permissions.
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	tenant := r.PathValue("tenant")
	role := r.PathValue("role")

	// §9.4: administrator authority is validated against the target tenant
	// before any write. This endpoint is tenant-scoped, not
	// hospital-scoped, so no hospital is asked about.
	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		tenant, ""); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant")
		return
	}

	var req saveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if len(req.Permissions) == 0 {
		writeError(w, http.StatusBadRequest, "permissions must not be empty")
		return
	}

	// §12.2: every permission leaf must resolve to a known catalog resource
	// and action. Checked before any write, and every row is checked -
	// reporting only the first would leave an administrator fixing one
	// typo at a time.
	var unknown []string
	for _, permission := range req.Permissions {
		if !h.Catalog.Has(permission.ResourceKey, permission.ActionKey) {
			unknown = append(unknown, permission.ResourceKey+":"+permission.ActionKey)
		}
	}
	if len(unknown) > 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("not in the active catalog: %v", unknown))
		return
	}

	now := h.now()
	before := make(map[string]bool, len(req.Permissions))
	inputs := make([]assignmentstore.RolePermissionInput, 0, len(req.Permissions))
	after := make(map[string]bool, len(req.Permissions))
	for _, permission := range req.Permissions {
		key := permission.ResourceKey + ":" + permission.ActionKey

		existing, found, err := h.Store.RolePermission(r.Context(), assignmentstore.RolePermissionKey{
			TenantID: tenant, RoleExternalID: role,
			ResourceKey: permission.ResourceKey, ActionKey: permission.ActionKey,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "reading the current role permission failed")
			return
		}
		if found {
			before[key] = existing.Enabled
		}

		var validUntil time.Time
		if permission.ValidUntil != nil {
			validUntil = *permission.ValidUntil
		}
		inputs = append(inputs, assignmentstore.RolePermissionInput{
			ResourceKey: permission.ResourceKey, ActionKey: permission.ActionKey,
			Enabled: permission.Enabled, ValidFrom: permission.ValidFrom, ValidUntil: validUntil,
		})
		after[key] = permission.Enabled
	}

	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)

	correlationID := r.Header.Get("X-Correlation-Id")
	if correlationID == "" {
		correlationID = h.newEventID()
	}

	// §10.1: the write has not committed yet, so this is the revision the
	// write WILL land on if it commits, not a revision already observed.
	// SaveRoleMatrix independently re-checks req.ExpectedRevision against
	// the tenant's actual current revision under its own lock, so this
	// number is only ever used once the write it describes has succeeded;
	// a stale ExpectedRevision means SaveRoleMatrix returns
	// ErrRevisionConflict before this row - or anything else in the same
	// transaction - is ever committed.
	newRevision := req.ExpectedRevision + 1

	// §10.2: one PermissionChanged per touched (resource, action) pair, so
	// a consumer invalidates exactly the cache keys that changed rather
	// than an entire role's or tenant's cache on any change.
	events := make([]permissionevents.PermissionChanged, 0, len(req.Permissions))
	for _, permission := range req.Permissions {
		events = append(events, permissionevents.PermissionChanged{
			EventID:     h.newEventID(),
			EventType:   permissionevents.EventTypePermissionChanged,
			TenantID:    tenant,
			SubjectType: permissionevents.SubjectRole,
			SubjectID:   role,
			Resource:    permission.ResourceKey,
			Action:      permission.ActionKey,
			Enabled:     permission.Enabled,
			Revision:    newRevision,
			OccurredAt:  now,
		})
	}
	payload, _ := json.Marshal(events)

	write := assignmentstore.RoleMatrixWrite{
		TenantID:         tenant,
		RoleExternalID:   role,
		ExpectedRevision: req.ExpectedRevision,
		Permissions:      inputs,
		Audit: assignmentstore.AuditEvent{
			EventID:       h.newEventID(),
			ActorID:       identity.PrincipalID,
			Operation:     "ROLE_MATRIX_SAVE",
			TargetType:    "role_permission",
			BeforeJSON:    string(beforeJSON),
			AfterJSON:     string(afterJSON),
			CorrelationID: correlationID,
			CreatedAt:     now,
		},
		Outbox: assignmentstore.OutboxEvent{
			EventID:      h.newEventID(),
			AggregateKey: tenant + ":" + role,
			EventType:    permissionevents.EventTypePermissionChanged,
			Payload:      string(payload),
			CreatedAt:    now,
		},
	}

	newRevision, err := h.Store.SaveRoleMatrix(r.Context(), write)
	if errors.Is(err, assignmentstore.ErrRevisionConflict) {
		writeError(w, http.StatusConflict, "expected revision is stale; reload the current matrix and retry")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "saving the role matrix failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"revision": newRevision})
}

// CurrentRevision handles GET /admin/authz/tenants/{tenant}/permission-revision,
// which a caller reads before its first Save to learn the expectedRevision
// to send.
func (h *Handler) CurrentRevision(w http.ResponseWriter, r *http.Request) {
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

	revision, found, err := h.Store.PermissionRevision(r.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading the permission revision failed")
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"revision": int64(0)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": revision.Revision})
}

// permissionView is one row of a GET .../permissions response.
type permissionView struct {
	ResourceKey string     `json:"resourceKey"`
	ActionKey   string     `json:"actionKey"`
	Enabled     bool       `json:"enabled"`
	ValidFrom   time.Time  `json:"validFrom"`
	ValidUntil  *time.Time `json:"validUntil,omitempty"`
}

// Read handles GET /admin/authz/tenants/{tenant}/roles/{role}/permissions
// (§9.4): the role matrix screen's read, returning every permission row
// the role carries exactly as stored, alongside the tenant's current
// revision so the caller has the expectedRevision its next Save needs.
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	tenant := r.PathValue("tenant")
	role := r.PathValue("role")
	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		tenant, ""); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant")
		return
	}

	permissions, err := h.Store.RolePermissionsForRole(r.Context(), tenant, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading the role's permissions failed")
		return
	}

	revision, found, err := h.Store.PermissionRevision(r.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading the permission revision failed")
		return
	}
	currentRevision := int64(0)
	if found {
		currentRevision = revision.Revision
	}

	views := make([]permissionView, 0, len(permissions))
	for _, permission := range permissions {
		view := permissionView{
			ResourceKey: permission.Key.ResourceKey, ActionKey: permission.Key.ActionKey,
			Enabled: permission.Enabled, ValidFrom: permission.ValidFrom,
		}
		if !permission.ValidUntil.IsZero() {
			validUntil := permission.ValidUntil
			view.ValidUntil = &validUntil
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"permissions": views,
		"revision":    currentRevision,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
