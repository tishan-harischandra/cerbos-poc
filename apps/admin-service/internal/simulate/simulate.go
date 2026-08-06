// Package simulate implements
// POST /admin/authz/simulate and POST /admin/authz/simulate-capabilities
// (§9.4, issue #19): the effective-access simulator that answers "why can
// this person do this" by running the real ADS and Cerbos path for an
// explicitly named principal, never a second evaluation implementation
// of its own (§21).
package simulate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/authority"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
)

// ADS is the narrow slice of the ADS's simulation surface this handler
// needs, satisfied by adsclient.Client in production and a fake in tests.
type ADS interface {
	SimulateAccess(ctx context.Context, token string, req adsclient.SimulateAccessRequest) (adsclient.SimulateAccessResponse, error)
	SimulateCapabilities(ctx context.Context, token string, req adsclient.SimulateCapabilitiesRequest) (adsclient.SimulateCapabilitiesResponse, error)
}

// Handler serves the simulator endpoints.
type Handler struct {
	ADS ADS
}

type accessRequest struct {
	TenantID    string           `json:"tenantId"`
	HospitalID  string           `json:"hospitalId"`
	PrincipalID string           `json:"principalId"`
	IdPRoles    []string         `json:"idpRoles"`
	Resource    accessRequestRes `json:"resource"`
	Action      string           `json:"action"`
}

type accessRequestRes struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
}

func (r accessRequest) validate() error {
	switch {
	case r.TenantID == "":
		return fmt.Errorf("tenantId is required")
	case r.HospitalID == "":
		return fmt.Errorf("hospitalId is required")
	case r.PrincipalID == "":
		return fmt.Errorf("principalId is required")
	case r.Resource.Kind == "":
		return fmt.Errorf("resource.kind is required")
	case r.Resource.ID == "":
		return fmt.Errorf("resource.id is required")
	case r.Action == "":
		return fmt.Errorf("action is required")
	}
	return nil
}

// SimulateAccess handles POST /admin/authz/simulate.
func (h *Handler) SimulateAccess(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	var req accessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		req.TenantID, req.HospitalID); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant and hospital")
		return
	}

	resp, err := h.ADS.SimulateAccess(r.Context(), identity.RawToken, adsclient.SimulateAccessRequest{
		TenantID: req.TenantID, HospitalID: req.HospitalID, PrincipalID: req.PrincipalID,
		IdPRoles: req.IdPRoles,
		Resource: adsclient.SimulateTarget{
			Kind: req.Resource.Kind, ID: req.Resource.ID, Attributes: req.Resource.Attributes,
		},
		Action: req.Action,
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the simulation could not be run")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

type capabilitiesRequest struct {
	Module           string                    `json:"module"`
	CapabilityKeys   []string                  `json:"capabilityKeys"`
	TenantID         string                    `json:"tenantId"`
	HospitalID       string                    `json:"hospitalId"`
	PrincipalID      string                    `json:"principalId"`
	IdPRoles         []string                  `json:"idpRoles"`
	SampleAttributes map[string]map[string]any `json:"sampleAttributes"`
}

func (r capabilitiesRequest) validate() error {
	switch {
	case r.Module == "":
		return fmt.Errorf("module is required")
	case len(r.CapabilityKeys) == 0:
		return fmt.Errorf("capabilityKeys must not be empty")
	case r.TenantID == "":
		return fmt.Errorf("tenantId is required")
	case r.HospitalID == "":
		return fmt.Errorf("hospitalId is required")
	case r.PrincipalID == "":
		return fmt.Errorf("principalId is required")
	}
	return nil
}

// SimulateCapabilities handles POST /admin/authz/simulate-capabilities.
func (h *Handler) SimulateCapabilities(w http.ResponseWriter, r *http.Request) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	var req capabilitiesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := authority.Validate(
		authority.Principal{TenantID: identity.TenantID, HospitalID: identity.HospitalID},
		req.TenantID, req.HospitalID); err != nil {
		writeError(w, http.StatusForbidden, "you do not have authority over this tenant and hospital")
		return
	}

	resp, err := h.ADS.SimulateCapabilities(r.Context(), identity.RawToken, adsclient.SimulateCapabilitiesRequest{
		Module: req.Module, CapabilityKeys: req.CapabilityKeys,
		TenantID: req.TenantID, HospitalID: req.HospitalID, PrincipalID: req.PrincipalID,
		IdPRoles: req.IdPRoles, SampleAttributes: req.SampleAttributes,
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the simulation could not be run")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
