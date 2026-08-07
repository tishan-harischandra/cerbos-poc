package authz

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// SimulateRequest is the administration simulator's request (issue #19,
// §9.4): unlike Request, the principal, tenant and hospital are named
// explicitly rather than derived from the caller's own verified token -
// the whole point of a simulator is answering "what would this other
// person see", not "what do I see".
type SimulateRequest struct {
	TenantID    string          `json:"tenantId"`
	HospitalID  string          `json:"hospitalId"`
	PrincipalID string          `json:"principalId"`
	IdPRoles    []string        `json:"idpRoles"`
	Resource    RequestResource `json:"resource"`
	Action      string          `json:"action"`
}

func (r SimulateRequest) validate() error {
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

// SimulateResponse is the simulator's answer for one resource-action.
type SimulateResponse struct {
	CerbosCallID       string `json:"cerbosCallId"`
	PermissionRevision int64  `json:"permissionRevision"`
	Allowed            bool   `json:"allowed"`
	Source             Source `json:"source"`
}

// NewSimulateHandler builds the POST /internal/authz/simulate handler
// (issue #19). It resolves assignments, assembles permissionContext, and
// asks the PDP through exactly the same Assignments and PDP collaborators
// the real decision endpoint uses, and labels the outcome with the same
// DecisionSource function - so a simulated answer is the runtime's own
// answer for the named principal, never a second evaluation
// implementation (§21).
//
// It is reachable only from other backend services over the internal
// compose network, never from a browser, the same trust boundary every
// other /internal/* route on this service already relies on.
func NewSimulateHandler(cfg Config) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := tokenauth.From(r.Context()); !ok {
			logger.ErrorContext(r.Context(), "the simulate endpoint was reached without a verified identity")
			writeError(w, http.StatusUnauthorized, "a bearer token is required")
			return
		}

		var req SimulateRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("malformed request: %v", err))
			return
		}
		if err := req.validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if role, found := reservedRole(req.IdPRoles); found {
			logger.ErrorContext(r.Context(), "a simulation named a reserved role",
				slog.String("role", role))
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("role %q is reserved for the platform", role))
			return
		}

		input, err := cfg.Assignments.For(r.Context(), AssignmentQuery{
			TenantID:     req.TenantID,
			HospitalID:   req.HospitalID,
			PrincipalID:  req.PrincipalID,
			ResourceKind: req.Resource.Kind,
			ResourceID:   req.Resource.ID,
			IdPRoles:     req.IdPRoles,
		})
		if err != nil {
			logger.ErrorContext(r.Context(), "resolving assignments for a simulation failed",
				slog.String("principalId", req.PrincipalID), slog.Any("error", err))
			writeError(w, http.StatusServiceUnavailable, "could not resolve permissions")
			return
		}

		assembled := permissioncontext.Assemble(input)
		ref := cerbosclient.ResourceRef{Kind: req.Resource.Kind, ID: req.Resource.ID}

		attr := make(map[string]any, len(req.Resource.Attributes)+1)
		for name, value := range req.Resource.Attributes {
			attr[name] = value
		}
		attr["permissionContext"] = assembled.AsMap()

		result, err := cfg.PDP.Check(r.Context(), cerbosclient.Request{
			Principal: cerbosclient.Principal{
				ID: req.PrincipalID,
				Attr: map[string]any{
					"tenantId":   req.TenantID,
					"hospitalId": req.HospitalID,
					"idpRoles":   req.IdPRoles,
				},
			},
			Resources: []cerbosclient.ResourceCheck{{
				Resource: ref,
				Attr:     attr,
				Actions:  []string{req.Action},
			}},
		})
		if err != nil {
			logger.ErrorContext(r.Context(), "the PDP could not be reached for a simulation",
				slog.Any("error", err))
			writeError(w, http.StatusServiceUnavailable, "could not reach the policy decision point")
			return
		}

		decision := result.Decisions[cerbosclient.Leaf{Resource: ref, Action: req.Action}]
		writeJSON(w, http.StatusOK, SimulateResponse{
			CerbosCallID:       result.CallID,
			PermissionRevision: assembled.PermissionRevision,
			Allowed:            decision.Allowed,
			Source:             DecisionSource(req.Action, decision.Allowed, result.FiredRules[ref]),
		})
	})
}
