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
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/canonicalid"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

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

// Metrics is where a served decision reports its outcome and latency
// (§17.1: request rate, allow/deny/error rate and latency by resource and
// action). Nil means nothing is reported.
type Metrics interface {
	Observe(resource, action, outcome string, latency time.Duration)
}

// Config holds the handler's collaborators.
type Config struct {
	PDP         PDP
	Assignments Assignments
	Logger      *slog.Logger
	Metrics     Metrics
	// RootPolicyRevision is the immutable root-policy tag this replica
	// currently serves, logged on every decision (§17.2).
	RootPolicyRevision string
}

// Request is the Appendix B decision request.
//
// It names resources and actions only. Principal, tenant, hospital and roles
// are derived from the verified token (§16.1), so there is no field here for a
// browser to put them in - and because unknown fields are refused, a caller
// that tries is told rather than quietly ignored.
type Request struct {
	Resources []RequestResource `json:"resources"`
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
// sets Allowed (§11.3). Source labels why, per Appendix A.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Source  Source `json:"source"`
}

type noopMetrics struct{}

func (noopMetrics) Observe(string, string, string, time.Duration) {}

// NewHandler builds the POST /internal/authz/check handler.
func NewHandler(cfg Config) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = noopMetrics{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// The identity is the middleware's, never the body's. A handler
		// reached without one has been mounted outside the authenticated
		// routes, which is a wiring defect rather than a caller error.
		identity, ok := tokenauth.From(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "the decision endpoint was reached without a verified identity")
			writeError(w, http.StatusUnauthorized, "a bearer token is required")
			return
		}

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

		// Defence in depth. Token verification already refuses a reserved
		// role, so reaching this is a bug in the wiring rather than an
		// attack that got through - but the synthetic role is the one claim
		// that must never reach the PDP from outside.
		if role, found := reservedRole(identity.Roles); found {
			logger.ErrorContext(r.Context(), "a verified identity carried a reserved role",
				slog.String("principalId", identity.PrincipalID),
				slog.String("role", role))
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("role %q is reserved for the platform", role))
			return
		}

		checkReq := cerbosclient.Request{
			Principal: cerbosclient.Principal{
				ID: identity.PrincipalID,
				Attr: map[string]any{
					"tenantId":   identity.TenantID,
					"hospitalId": identity.HospitalID,
					"idpRoles":   identity.Roles,
				},
			},
		}

		revisions := make(map[cerbosclient.ResourceRef]int64, len(req.Resources))
		for _, resource := range req.Resources {
			input, err := cfg.Assignments.For(r.Context(), AssignmentQuery{
				TenantID:     identity.TenantID,
				HospitalID:   identity.HospitalID,
				PrincipalID:  identity.PrincipalID,
				ResourceKind: resource.Kind,
				ResourceID:   resource.ID,
				IdPRoles:     identity.Roles,
			})
			if err != nil {
				logger.ErrorContext(r.Context(), "resolving assignments failed",
					slog.String("principalId", identity.PrincipalID),
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
				slog.String("principalId", identity.PrincipalID),
				slog.Any("error", err))
			latency := time.Since(start)
			for _, resource := range req.Resources {
				for _, action := range resource.Actions {
					metrics.Observe(resource.Kind, action, "error", latency)
				}
			}
			writeError(w, http.StatusServiceUnavailable, "could not reach the policy decision point")
			return
		}

		latency := time.Since(start)
		response := Response{CerbosCallID: result.CallID}
		for _, resource := range req.Resources {
			ref := cerbosclient.ResourceRef{Kind: resource.Kind, ID: resource.ID}
			actions := make(map[string]Decision, len(resource.Actions))
			for _, action := range resource.Actions {
				// A leaf the PDP did not answer for stays denied.
				decision := result.Decisions[cerbosclient.Leaf{Resource: ref, Action: action}]
				actions[action] = Decision{
					Allowed: decision.Allowed,
					Source:  DecisionSource(action, decision.Allowed, result.FiredRules[ref]),
				}
				outcome := "deny"
				if decision.Allowed {
					outcome = "allow"
				}
				metrics.Observe(resource.Kind, action, outcome, latency)
			}
			response.Resources = append(response.Resources, ResourceDecision{
				Kind:               resource.Kind,
				ID:                 resource.ID,
				PermissionRevision: revisions[ref],
				Actions:            actions,
			})
		}

		// §11.3, §17.2: the Cerbos call ID is logged next to the
		// application correlation ID so a decision can be traced into the
		// PDP audit log, alongside the permission and root policy
		// revisions and the idp roles a decision was matched against - a
		// decision can be reconstructed end-to-end from this one record
		// without a second lookup. No resource attribute or clinical
		// value is logged here (§16.2).
		loggedRevisions := make(map[string]int64, len(revisions))
		for ref, revision := range revisions {
			loggedRevisions[ref.Kind+":"+ref.ID] = revision
		}
		logger.InfoContext(r.Context(), "authorization decision served",
			slog.String("correlationId", correlationID(r)),
			slog.String("cerbosCallId", result.CallID),
			slog.String("principalId", identity.PrincipalID),
			slog.String("tenantId", identity.TenantID),
			slog.String("rootPolicyRevision", cfg.RootPolicyRevision),
			slog.Any("permissionRevisions", loggedRevisions),
			slog.Any("roleIds", identity.Roles))

		writeJSON(w, http.StatusOK, response)
	})
}

func (r Request) validate() error {
	if len(r.Resources) == 0 {
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
		if canonicalid.IsReserved(role) {
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
