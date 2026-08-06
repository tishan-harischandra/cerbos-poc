// Package pep is the resource service's policy enforcement point (issue #9).
//
// Every handler follows the same shape: load the resource's trusted
// attributes itself, ask the ADS to decide (never the PDP directly - the ADS
// already owns that call, §21), and only ever act on an explicit allow. No
// handler reads a tenant, hospital or permissionContext value out of the
// request body; those are either derived from the verified identity or read
// back from the store.
package pep

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Store is the part of assignmentstore.Store the PEP needs. Narrow on
// purpose, the same reasoning demoseed.Writer already uses: a handler that
// took the whole port could quietly grow into something else.
type Store interface {
	SaveResource(ctx context.Context, resource assignmentstore.Resource) error
	Resource(ctx context.Context, resourceType, resourceID string) (assignmentstore.Resource, bool, error)
	DeleteResource(ctx context.Context, resourceType, resourceID string) error
	ListResources(ctx context.Context, query assignmentstore.ListResourcesQuery) ([]assignmentstore.Resource, int, error)
}

// ADS is the decision endpoint this service enforces every action against.
type ADS interface {
	Check(ctx context.Context, token string, checks []adsclient.ResourceCheck) ([]adsclient.ResourceDecision, error)
}

// IDGenerator produces a new resource ID for Create. It is a field so tests
// can make IDs deterministic.
type IDGenerator func() string

// Config holds the handler's collaborators.
type Config struct {
	Store  Store
	ADS    ADS
	Logger *slog.Logger
	Clock  func() time.Time
	NewID  IDGenerator
}

type resourceEnvelope struct {
	Kind               string                        `json:"kind"`
	ID                 string                        `json:"id"`
	PermissionRevision int64                         `json:"permissionRevision"`
	Actions            map[string]adsclient.Decision `json:"actions"`
	Resource           json.RawMessage               `json:"resource,omitempty"`
}

type listEnvelope struct {
	Resources []resourceEnvelope `json:"resources"`
	Total     int                `json:"total"`
}

// NewHandler builds the generic /fhir/{type}... surface.
func NewHandler(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = randomID
	}

	h := &handler{cfg: cfg}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /fhir/{type}", h.create)
	mux.HandleFunc("GET /fhir/{type}", h.list)
	mux.HandleFunc("GET /fhir/{type}/{id}", h.read)
	mux.HandleFunc("PUT /fhir/{type}/{id}", h.update)
	mux.HandleFunc("DELETE /fhir/{type}/{id}", h.delete)
	mux.HandleFunc("POST /fhir/{type}/{id}/assign", h.assign)
	return mux
}

type handler struct {
	cfg Config
}

// requireIdentity reports the caller's verified identity or writes an error
// and returns false. Every handler starts here: nothing below trusts a
// request body for who the caller is.
func (h *handler) requireIdentity(w http.ResponseWriter, r *http.Request) (tokenauth.Identity, bool) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		h.cfg.Logger.ErrorContext(r.Context(), "the resource service was reached without a verified identity")
		writeError(w, http.StatusUnauthorized, "a bearer token is required")
		return tokenauth.Identity{}, false
	}
	return identity, true
}

// decide asks the ADS about exactly one resource and action. It reports the
// decision for that action and the permission revision it was taken
// against, so a caller can log or display which revision of the matrix
// produced the answer (§11.3). Every mutating handler calls this before
// touching the store.
func (h *handler) decide(ctx context.Context, token string, resourceType, resourceID string,
	attr map[string]any, action string,
) (adsclient.Decision, int64, error) {
	decisions, err := h.cfg.ADS.Check(ctx, token, []adsclient.ResourceCheck{
		{Kind: resourceType, ID: resourceID, Attributes: attr, Actions: []string{action}},
	})
	if err != nil {
		return adsclient.Decision{}, 0, err
	}
	if len(decisions) == 0 {
		return adsclient.Decision{}, 0, nil
	}
	return decisions[0].Actions[action], decisions[0].PermissionRevision, nil
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	resourceType := r.PathValue("type")

	body, err := readJSONObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The tenant and hospital a new instance is created in come from the
	// verified identity, never from the request body (§16.1): a caller
	// naming a different tenant could otherwise create a resource for a
	// tenant it holds no permission in.
	id := h.cfg.NewID()
	attr := map[string]any{
		"tenantId":   identity.TenantID,
		"hospitalId": identity.HospitalID,
		"status":     "ACTIVE",
	}

	decision, revision, err := h.decide(r.Context(), identity.RawToken, resourceType, id, attr, "create")
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "the ADS could not be reached", slog.Any("error", err))
		writeError(w, http.StatusServiceUnavailable, "could not reach the decision service")
		return
	}
	if !decision.Allowed {
		writeEnvelope(w, http.StatusForbidden, resourceEnvelope{
			Kind: resourceType, ID: id,
			Actions: map[string]adsclient.Decision{"create": decision},
		})
		return
	}

	payload, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encode the resource payload")
		return
	}

	resource := assignmentstore.Resource{
		ResourceType: resourceType,
		ResourceID:   id,
		TenantID:     identity.TenantID,
		HospitalID:   identity.HospitalID,
		Status:       "ACTIVE",
		PayloadJSON:  string(payload),
		UpdatedAt:    h.cfg.Clock(),
	}
	if err := h.cfg.Store.SaveResource(r.Context(), resource); err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "saving the created resource failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not save the resource")
		return
	}

	writeEnvelope(w, http.StatusCreated, envelopeFor(resource, revision,
		map[string]adsclient.Decision{"create": decision}))
}

func (h *handler) read(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	resourceType, id := r.PathValue("type"), r.PathValue("id")

	resource, found, err := h.cfg.Store.Resource(r.Context(), resourceType, id)
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "reading the resource failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not read the resource")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such resource")
		return
	}

	decision, revision, err := h.decide(r.Context(), identity.RawToken, resourceType, id, attrOf(resource), "read")
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "the ADS could not be reached", slog.Any("error", err))
		writeError(w, http.StatusServiceUnavailable, "could not reach the decision service")
		return
	}
	if !decision.Allowed {
		writeEnvelope(w, http.StatusForbidden, resourceEnvelope{
			Kind: resourceType, ID: id,
			Actions: map[string]adsclient.Decision{"read": decision},
		})
		return
	}

	writeEnvelope(w, http.StatusOK, envelopeFor(resource, revision, map[string]adsclient.Decision{"read": decision}))
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	resourceType, id := r.PathValue("type"), r.PathValue("id")

	existing, found, err := h.cfg.Store.Resource(r.Context(), resourceType, id)
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "reading the resource failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not read the resource")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such resource")
		return
	}

	body, err := readJSONObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	decision, revision, err := h.decide(r.Context(), identity.RawToken, resourceType, id, attrOf(existing), "update")
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "the ADS could not be reached", slog.Any("error", err))
		writeError(w, http.StatusServiceUnavailable, "could not reach the decision service")
		return
	}
	if !decision.Allowed {
		writeEnvelope(w, http.StatusForbidden, resourceEnvelope{
			Kind: resourceType, ID: id,
			Actions: map[string]adsclient.Decision{"update": decision},
		})
		return
	}

	// The tenant, hospital and status are the store's own truth, not the
	// request body's: a caller that could rewrite tenantId on update could
	// move a resource out of the isolation boundary it was just checked
	// against.
	if status, ok := body["status"].(string); ok {
		existing.Status = status
	}
	if department, ok := body["department"].(string); ok {
		existing.Department = department
	}
	if sensitivity, ok := body["sensitivity"].(string); ok {
		existing.Sensitivity = sensitivity
	}
	payload, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encode the resource payload")
		return
	}
	existing.PayloadJSON = string(payload)
	existing.UpdatedAt = h.cfg.Clock()

	if err := h.cfg.Store.SaveResource(r.Context(), existing); err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "saving the updated resource failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not save the resource")
		return
	}

	writeEnvelope(w, http.StatusOK, envelopeFor(existing, revision, map[string]adsclient.Decision{"update": decision}))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	resourceType, id := r.PathValue("type"), r.PathValue("id")

	existing, found, err := h.cfg.Store.Resource(r.Context(), resourceType, id)
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "reading the resource failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not read the resource")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such resource")
		return
	}

	decision, _, err := h.decide(r.Context(), identity.RawToken, resourceType, id, attrOf(existing), "delete")
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "the ADS could not be reached", slog.Any("error", err))
		writeError(w, http.StatusServiceUnavailable, "could not reach the decision service")
		return
	}
	if !decision.Allowed {
		writeEnvelope(w, http.StatusForbidden, resourceEnvelope{
			Kind: resourceType, ID: id,
			Actions: map[string]adsclient.Decision{"delete": decision},
		})
		return
	}

	if err := h.cfg.Store.DeleteResource(r.Context(), resourceType, id); err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "deleting the resource failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not delete the resource")
		return
	}

	writeEnvelope(w, http.StatusOK, resourceEnvelope{
		Kind: resourceType, ID: id,
		Actions: map[string]adsclient.Decision{"delete": decision},
	})
}

// assign transfers responsibility for an instance. It is authorized as its
// own action, independent of update (§6, issue #8): a grant of update does
// not imply a grant of assign, and the reverse.
func (h *handler) assign(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	resourceType, id := r.PathValue("type"), r.PathValue("id")

	existing, found, err := h.cfg.Store.Resource(r.Context(), resourceType, id)
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "reading the resource failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not read the resource")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such resource")
		return
	}

	body, err := readJSONObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	decision, revision, err := h.decide(r.Context(), identity.RawToken, resourceType, id, attrOf(existing), "assign")
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "the ADS could not be reached", slog.Any("error", err))
		writeError(w, http.StatusServiceUnavailable, "could not reach the decision service")
		return
	}
	if !decision.Allowed {
		writeEnvelope(w, http.StatusForbidden, resourceEnvelope{
			Kind: resourceType, ID: id,
			Actions: map[string]adsclient.Decision{"assign": decision},
		})
		return
	}

	var payload map[string]any
	if len(existing.PayloadJSON) > 0 {
		if err := json.Unmarshal([]byte(existing.PayloadJSON), &payload); err != nil {
			payload = map[string]any{}
		}
	} else {
		payload = map[string]any{}
	}
	if assignedTo, ok := body["assignedTo"]; ok {
		payload["assignedTo"] = assignedTo
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encode the resource payload")
		return
	}
	existing.PayloadJSON = string(encoded)
	existing.UpdatedAt = h.cfg.Clock()

	if err := h.cfg.Store.SaveResource(r.Context(), existing); err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "saving the assignment failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not save the resource")
		return
	}

	writeEnvelope(w, http.StatusOK, envelopeFor(existing, revision, map[string]adsclient.Decision{"assign": decision}))
}

// defaultListLimit mirrors assignmentstore.DefaultListLimit; kept local so
// this package's request parsing does not need to import the store package
// just for a number.
const defaultListLimit = 50

// maxListLimit bounds how large a single page a caller may ask for. Without
// a ceiling, a caller could request a page large enough that authorizing it
// would need many chunked ADS calls for one HTTP request.
const maxListLimit = 500

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	resourceType := r.PathValue("type")

	limit := defaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := parsePositiveInt(raw); err == nil && parsed > 0 {
			limit = min(parsed, maxListLimit)
		}
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := parsePositiveInt(raw); err == nil {
			offset = parsed
		}
	}

	page, total, err := h.cfg.Store.ListResources(r.Context(), assignmentstore.ListResourcesQuery{
		ResourceType: resourceType,
		TenantID:     identity.TenantID,
		HospitalID:   identity.HospitalID,
		Limit:        limit,
		Offset:       offset,
	})
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "listing resources failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "could not list resources")
		return
	}

	if len(page) == 0 {
		writeEnvelope(w, http.StatusOK, listEnvelope{Resources: nil, Total: total})
		return
	}

	// One batched question to the ADS for the whole page (chunked
	// automatically past adsclient.MaxResourcesPerRequest), not one HTTP
	// round trip per row (issue #9's "bounded number of requests, not N").
	checks := make([]adsclient.ResourceCheck, 0, len(page))
	for _, resource := range page {
		checks = append(checks, adsclient.ResourceCheck{
			Kind: resource.ResourceType, ID: resource.ResourceID,
			Attributes: attrOf(resource), Actions: []string{"read"},
		})
	}
	decisions, err := h.cfg.ADS.Check(r.Context(), identity.RawToken, checks)
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "the ADS could not be reached", slog.Any("error", err))
		writeError(w, http.StatusServiceUnavailable, "could not reach the decision service")
		return
	}
	decisionByID := make(map[string]adsclient.ResourceDecision, len(decisions))
	for _, d := range decisions {
		decisionByID[d.ID] = d
	}

	envelopes := make([]resourceEnvelope, 0, len(page))
	for _, resource := range page {
		d, found := decisionByID[resource.ResourceID]
		actions := map[string]adsclient.Decision{"read": {}}
		if found {
			actions = d.Actions
		}
		if !actions["read"].Allowed {
			// A row this caller may not read is reported denied, without
			// its payload, rather than left out: a caller distinguishing
			// "denied" from "not on this page" is exactly the row-level
			// decision §12.7 asks for.
			envelopes = append(envelopes, resourceEnvelope{
				Kind: resource.ResourceType, ID: resource.ResourceID, Actions: actions,
			})
			continue
		}
		envelopes = append(envelopes, envelopeFor(resource, d.PermissionRevision, actions))
	}

	writeEnvelope(w, http.StatusOK, listEnvelope{Resources: envelopes, Total: total})
}

func attrOf(resource assignmentstore.Resource) map[string]any {
	return map[string]any{
		"tenantId":   resource.TenantID,
		"hospitalId": resource.HospitalID,
		"status":     resource.Status,
	}
}

func envelopeFor(resource assignmentstore.Resource, revision int64, actions map[string]adsclient.Decision) resourceEnvelope {
	payload := json.RawMessage(resource.PayloadJSON)
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	return resourceEnvelope{
		Kind: resource.ResourceType, ID: resource.ResourceID,
		PermissionRevision: revision,
		Actions:            actions,
		Resource:           payload,
	}
}

func readJSONObject(r *http.Request) (map[string]any, error) {
	if r.Body == nil {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(r.Body)
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("malformed request body: %w", err)
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

func writeEnvelope(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeEnvelope(w, status, map[string]string{"error": message})
}

func parsePositiveInt(raw string) (int, error) {
	var value int
	_, err := fmt.Sscanf(raw, "%d", &value)
	return value, err
}

func randomID() string {
	return fmt.Sprintf("res-%d", time.Now().UnixNano())
}
