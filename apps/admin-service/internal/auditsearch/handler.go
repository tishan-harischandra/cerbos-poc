// Package auditsearch implements GET /admin/authz/audit (§9.1, §9.4): the
// searchable administration audit, kept separate from the authorization
// decision audit but joinable to it by correlation ID (§17.2, §17.3).
//
// There is deliberately no write path here. The audit is append-only
// (§8.2): assignmentstore.Store.AppendAuditEvent is the only way a row is
// ever written, and it is called from the role-matrix and user-override
// save paths, never from this package.
package auditsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/authority"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Store is the narrow slice of assignmentstore.Store this handler needs.
type Store interface {
	SearchAuditEvents(ctx context.Context, query assignmentstore.AuditEventSearchQuery) (page []assignmentstore.AuditEvent, totalCount int, err error)
}

// Handler serves the audit search endpoint.
type Handler struct {
	Store Store
}

// eventView is one row of a GET /admin/authz/audit response. Only the
// fields §9.1's audit search dimensions and §17.3's administration audit
// category need are exposed: before/after JSON never carries clinical
// attributes (§9.4's before/after states are booleans and effect labels,
// never resource payloads), so nothing here needs redacting.
type eventView struct {
	EventID        string    `json:"eventId"`
	ActorID        string    `json:"actorId"`
	Operation      string    `json:"operation"`
	TargetType     string    `json:"targetType"`
	BeforeJSON     string    `json:"before"`
	AfterJSON      string    `json:"after"`
	TenantID       string    `json:"tenantId"`
	HospitalID     string    `json:"hospitalId,omitempty"`
	RoleExternalID string    `json:"roleExternalId,omitempty"`
	TargetUserID   string    `json:"targetUserId,omitempty"`
	ResourceKeys   string    `json:"resourceActionKeys,omitempty"`
	CorrelationID  string    `json:"correlationId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// Search handles GET /admin/authz/audit?tenant=&hospital=&actor=&role=&
// user=&resource=&action=&from=&to=&limit=&offset= (§9.4's audit search
// endpoint).
//
// tenant is a required query parameter rather than a path segment, per
// §9.4's endpoint shape, but it is still mandatory: an administration
// query without a tenant predicate is exactly the mistake §8.2 warns
// against, so an absent or unauthorized tenant is refused before the
// store is ever asked.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	query := r.URL.Query()
	tenant := query.Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "a tenant query parameter is required")
		return
	}
	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		tenant, ""); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant")
		return
	}

	search := assignmentstore.AuditEventSearchQuery{
		TenantID:       tenant,
		HospitalID:     query.Get("hospital"),
		ActorID:        query.Get("actor"),
		RoleExternalID: query.Get("role"),
		TargetUserID:   query.Get("user"),
		ResourceKey:    query.Get("resource"),
		ActionKey:      query.Get("action"),
	}

	if from := query.Get("from"); from != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from must be an RFC3339 timestamp")
			return
		}
		search.CreatedFrom = parsed
	}
	if to := query.Get("to"); to != "" {
		parsed, err := time.Parse(time.RFC3339, to)
		if err != nil {
			writeError(w, http.StatusBadRequest, "to must be an RFC3339 timestamp")
			return
		}
		search.CreatedTo = parsed
	}
	if limit := query.Get("limit"); limit != "" {
		parsed, err := strconv.Atoi(limit)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		search.Limit = parsed
	}
	if offset := query.Get("offset"); offset != "" {
		parsed, err := strconv.Atoi(offset)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		search.Offset = parsed
	}

	events, total, err := h.Store.SearchAuditEvents(r.Context(), search)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "searching the audit history failed")
		return
	}

	views := make([]eventView, 0, len(events))
	for _, event := range events {
		views = append(views, eventView{
			EventID: event.EventID, ActorID: event.ActorID, Operation: event.Operation,
			TargetType: event.TargetType, BeforeJSON: event.BeforeJSON, AfterJSON: event.AfterJSON,
			TenantID: event.TenantID, HospitalID: event.HospitalID,
			RoleExternalID: event.RoleExternalID, TargetUserID: event.TargetUserID,
			ResourceKeys: event.ResourceActionKeys, CorrelationID: event.CorrelationID,
			CreatedAt: event.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":     views,
		"totalCount": total,
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
