// Package authz serves the ADS runtime decision endpoint.
//
// Its job is to assemble trusted data, ask the PDP, and report what the PDP
// said. It does not rank grants against revokes, and it does not report *why*
// an action was allowed: deriving a decision source such as USER_REVOKE in Go
// would mean re-deciding precedence here, which is the duplicated-logic failure
// mode §21 warns about. Precedence lives in Cerbos policy (§6.3, ADR-003).
package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// ReservedRolePrefix marks roles only the platform may assign. A token
// presenting one is refused before a decision is attempted (§21).
const ReservedRolePrefix = "sys:"

// PDP is the policy decision point the handler asks.
type PDP interface {
	Check(ctx context.Context, req cerbosclient.Request) (cerbosclient.Result, error)
}

// AssignmentQuery identifies whose permissions to resolve for which resource.
type AssignmentQuery struct {
	TenantID     string
	HospitalID   string
	PrincipalID  string
	ResourceKind string
	ResourceID   string
	IdPRoles     []string
}

// Assignments resolves the role permissions and user overrides that apply to
// one principal and one resource. It returns data for Cerbos to judge.
type Assignments interface {
	For(ctx context.Context, query AssignmentQuery) (permissioncontext.Input, error)
}

// Config holds the handler's collaborators.
type Config struct {
	PDP         PDP
	Assignments Assignments
	Logger      *slog.Logger
}

// Request is the Appendix B decision request.
type Request struct {
	TenantID    string            `json:"tenantId"`
	HospitalID  string            `json:"hospitalId"`
	PrincipalID string            `json:"principalId"`
	IdPRoles    []string          `json:"idpRoles"`
	Resources   []RequestResource `json:"resources"`
}

// RequestResource is one resource and the actions asked about it.
type RequestResource struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
	Actions    []string       `json:"actions"`
}

// Response reports one decision per requested action.
type Response struct {
	CerbosCallID string             `json:"cerbosCallId"`
	Resources    []ResourceDecision `json:"resources"`
}

// ResourceDecision is the outcome for one resource.
type ResourceDecision struct {
	Kind               string              `json:"kind"`
	ID                 string              `json:"id"`
	PermissionRevision int64               `json:"permissionRevision"`
	Actions            map[string]Decision `json:"actions"`
}

// Decision is the outcome for one action. Only an explicit allow from the PDP
// sets Allowed (§11.3).
type Decision struct {
	Allowed bool `json:"allowed"`
}

// NewHandler builds the POST /internal/authz/check handler.
func NewHandler(cfg Config) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
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
			logger.WarnContext(r.Context(), "rejected a token presenting a reserved role",
				slog.String("principalId", req.PrincipalID),
				slog.String("role", role))
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("role %q is reserved for the platform", role))
			return
		}

		checkReq := cerbosclient.Request{
			Principal: cerbosclient.Principal{
				ID: req.PrincipalID,
				Attr: map[string]any{
					"tenantId":   req.TenantID,
					"hospitalId": req.HospitalID,
					"idpRoles":   req.IdPRoles,
				},
			},
		}

		revisions := make(map[cerbosclient.ResourceRef]int64, len(req.Resources))
		for _, resource := range req.Resources {
			input, err := cfg.Assignments.For(r.Context(), AssignmentQuery{
				TenantID:     req.TenantID,
				HospitalID:   req.HospitalID,
				PrincipalID:  req.PrincipalID,
				ResourceKind: resource.Kind,
				ResourceID:   resource.ID,
				IdPRoles:     req.IdPRoles,
			})
			if err != nil {
				logger.ErrorContext(r.Context(), "resolving assignments failed",
					slog.String("principalId", req.PrincipalID),
					slog.String("resourceId", resource.ID),
					slog.Any("error", err))
				writeError(w, http.StatusServiceUnavailable, "could not resolve permissions")
				return
			}

			assembled := permissioncontext.Assemble(input)
			ref := cerbosclient.ResourceRef{Kind: resource.Kind, ID: resource.ID}
			revisions[ref] = assembled.PermissionRevision

			attr := make(map[string]any, len(resource.Attributes)+1)
			for name, value := range resource.Attributes {
				attr[name] = value
			}
			// AsMap, not the struct: Cerbos carries attributes as protobuf
			// Struct values and a Go struct would be dropped in transit.
			attr["permissionContext"] = assembled.AsMap()

			checkReq.Resources = append(checkReq.Resources, cerbosclient.ResourceCheck{
				Resource: ref,
				Attr:     attr,
				Actions:  resource.Actions,
			})
		}

		result, err := cfg.PDP.Check(r.Context(), checkReq)
		if err != nil {
			logger.ErrorContext(r.Context(), "the PDP could not be reached",
				slog.String("principalId", req.PrincipalID),
				slog.Any("error", err))
			writeError(w, http.StatusServiceUnavailable, "could not reach the policy decision point")
			return
		}

		response := Response{CerbosCallID: result.CallID}
		for _, resource := range req.Resources {
			ref := cerbosclient.ResourceRef{Kind: resource.Kind, ID: resource.ID}
			actions := make(map[string]Decision, len(resource.Actions))
			for _, action := range resource.Actions {
				// A leaf the PDP did not answer for stays denied.
				decision := result.Decisions[cerbosclient.Leaf{Resource: ref, Action: action}]
				actions[action] = Decision{Allowed: decision.Allowed}
			}
			response.Resources = append(response.Resources, ResourceDecision{
				Kind:               resource.Kind,
				ID:                 resource.ID,
				PermissionRevision: revisions[ref],
				Actions:            actions,
			})
		}

		// §11.3: the Cerbos call ID is logged next to the application
		// correlation ID so a decision can be traced into the PDP audit log.
		logger.InfoContext(r.Context(), "authorization decision served",
			slog.String("correlationId", correlationID(r)),
			slog.String("cerbosCallId", result.CallID),
			slog.String("principalId", req.PrincipalID),
			slog.String("tenantId", req.TenantID))

		writeJSON(w, http.StatusOK, response)
	})
}

func (r Request) validate() error {
	switch {
	case r.TenantID == "":
		return errors.New("tenantId is required")
	case r.HospitalID == "":
		return errors.New("hospitalId is required")
	case r.PrincipalID == "":
		return errors.New("principalId is required")
	case len(r.Resources) == 0:
		return errors.New("at least one resource is required")
	}

	for i, resource := range r.Resources {
		switch {
		case resource.Kind == "":
			return fmt.Errorf("resources[%d].kind is required", i)
		case resource.ID == "":
			return fmt.Errorf("resources[%d].id is required", i)
		case len(resource.Actions) == 0:
			return fmt.Errorf("resources[%d].actions must not be empty", i)
		}
	}
	return nil
}

func reservedRole(roles []string) (string, bool) {
	for _, role := range roles {
		if strings.HasPrefix(strings.ToLower(role), ReservedRolePrefix) {
			return role, true
		}
	}
	return "", false
}

// CorrelationHeader carries the caller's correlation ID.
const CorrelationHeader = "X-Correlation-Id"

func correlationID(r *http.Request) string {
	return r.Header.Get(CorrelationHeader)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
