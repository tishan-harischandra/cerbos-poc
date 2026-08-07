// Package capability serves the ADS's capability snapshot endpoint: the
// §12.3 backend evaluation algorithm end to end (issue #11, ADR-005).
//
// It resolves every requested capability's targetRefs server-side, batches
// the resulting permission leaves into a bounded number of Cerbos calls,
// and composes the results with libs/capabilityeval - the same pure
// evaluator, unit-tested on its own without any of this package's
// collaborators. This package's own job is assembling trusted data and
// calling the PDP; it never ranks a grant against a revoke itself (§6.3,
// ADR-003), and it never trusts a browser-supplied resource attribute -
// only routing identifiers travel in the request body, exactly like the
// existing decision endpoint (apps/ads/internal/authz).
package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// DefaultMaxResourcesPerCheck bounds how many resources one Cerbos
// CheckResources call carries (§15.2: "batch...within Cerbos request
// limits"). Chosen well under Cerbos's own default request-size limits so
// a large module snapshot chunks automatically instead of ever depending
// on the PDP's own rejection behaviour.
const DefaultMaxResourcesPerCheck = 500

// PDP is the policy decision point the handler asks.
type PDP interface {
	Check(ctx context.Context, req cerbosclient.Request) (cerbosclient.Result, error)
}

// CapabilityCatalog supplies the UiCapabilityDefinitions for one module at
// the active capability-catalog revision (§12.3 step 1).
type CapabilityCatalog interface {
	Definitions(ctx context.Context, module string) (defs []capabilitycatalog.UiCapabilityDefinition, catalogRevision string, err error)
}

// TargetQuery identifies one symbolic targetRef that needs resolving into a
// concrete resource (§12.3 step 2).
type TargetQuery struct {
	TenantID     string
	HospitalID   string
	ResourceKind string
	TargetRef    string
	// RouteContext is the browser-supplied routing context (e.g.
	// {"patientId": "patient-456"}). It carries identifiers only, never
	// trust attributes: the resolver loads the resource itself and derives
	// its authorization attributes from the authoritative source (§12.3).
	RouteContext map[string]string
}

// ResolvedTarget is a targetRef resolved to a concrete resource and its
// trusted, server-loaded attributes.
type ResolvedTarget struct {
	Resource   capabilityeval.ResourceRef
	Attributes map[string]any
}

// TargetResolver resolves symbolic targetRefs into concrete resources
// server-side. The browser cannot supply a resource's authorization
// attributes directly; it can only name which instance it is looking at.
type TargetResolver interface {
	Resolve(ctx context.Context, query TargetQuery) (ResolvedTarget, error)
}

// Assignments resolves the role permissions and user overrides that apply
// to one principal and one resource (reuses the decision endpoint's port).
type Assignments interface {
	For(ctx context.Context, query authz.AssignmentQuery) (permissioncontext.Input, error)
}

// Audience selects how much failure evidence a snapshot carries.
//
// §12.4: "Failure evidence is optional and should be filtered for the
// audience. End-user responses normally contain a stable reason code,
// while the administration simulator may expose the complete requirement
// tree." The zero value is AudienceEndUser, so a Config that forgets to
// set this field fails safe rather than leaking the requirement tree.
type Audience int

const (
	// AudienceEndUser carries a stable reason code only. Full requirement
	// trees must never appear on this path (issue #11 acceptance criteria).
	AudienceEndUser Audience = iota
	// AudienceAdmin additionally carries FailedRequirements, for the
	// administration simulator.
	AudienceAdmin
)

// Config holds the handler's collaborators.
type Config struct {
	PDP               PDP
	CapabilityCatalog CapabilityCatalog
	TargetResolver    TargetResolver
	Assignments       Assignments
	// RootPolicyRevision is the immutable root-policy tag currently served
	// (§12.4, §13.1), e.g. "root-v1.4.0". Static per deployment.
	RootPolicyRevision string
	// MaxResourcesPerCheck bounds resources per Cerbos call. Zero means
	// DefaultMaxResourcesPerCheck.
	MaxResourcesPerCheck int
	// Audience controls failure-evidence detail. Zero value is
	// AudienceEndUser.
	Audience Audience
	Logger   *slog.Logger
}

// Request is the §12.3 capability-evaluation request. Only routing
// identifiers travel here - tenant, hospital and principal come from the
// verified token, never the body, exactly like the decision endpoint's
// Request (§12.3: "The browser must not provide trusted permission
// decisions, tenant ownership or user overrides").
type Request struct {
	Module         string            `json:"module"`
	CapabilityKeys []string          `json:"capabilityKeys"`
	Context        map[string]string `json:"context"`
}

func (r Request) validate() error {
	if r.Module == "" {
		return errors.New("module is required")
	}
	if len(r.CapabilityKeys) == 0 {
		return errors.New("capabilityKeys must not be empty")
	}
	return nil
}

// Snapshot is the §12.4 capability snapshot.
type Snapshot struct {
	AuthorizationRevision     int64                       `json:"authorizationRevision"`
	RootPolicyRevision        string                      `json:"rootPolicyRevision"`
	CapabilityCatalogRevision string                      `json:"capabilityCatalogRevision"`
	TenantID                  string                      `json:"tenantId"`
	HospitalID                string                      `json:"hospitalId"`
	Module                    string                      `json:"module"`
	ContextFingerprint        string                      `json:"contextFingerprint"`
	Capabilities              map[string]CapabilityResult `json:"capabilities"`
}

// CapabilityResult is one capability's decision in the administration
// shape. Handlers serving end-user audiences must call ForEndUser instead
// of encoding this type directly (issue #11 acceptance criteria: "full
// requirement trees never appear on end-user paths").
type CapabilityResult struct {
	Allowed            bool                `json:"allowed"`
	Reason             string              `json:"reason,omitempty"`
	FailedRequirements []FailedRequirement `json:"failedRequirements,omitempty"`
}

// FailedRequirement is one denied leaf that explains a capability's denial,
// administration-audience evidence only (§12.4).
type FailedRequirement struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Target   string `json:"target"`
	Reason   string `json:"reason"`
}

// NewHandler builds the POST /internal/capabilities/evaluate handler.
func NewHandler(cfg Config) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	maxResources := cfg.MaxResourcesPerCheck
	if maxResources <= 0 {
		maxResources = DefaultMaxResourcesPerCheck
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := tokenauth.From(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "the capability endpoint was reached without a verified identity")
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

		snapshot, _, err := evaluate(r.Context(), cfg, maxResources, identity, req)
		if err != nil {
			var badRequest *badRequestError
			if errors.As(err, &badRequest) {
				writeError(w, http.StatusBadRequest, badRequest.Error())
				return
			}
			logger.ErrorContext(r.Context(), "evaluating capabilities failed",
				slog.String("principalId", identity.PrincipalID),
				slog.String("module", req.Module),
				slog.Any("error", err))
			writeError(w, http.StatusServiceUnavailable, "could not evaluate capabilities")
			return
		}

		writeJSON(w, http.StatusOK, snapshot)
	})
}

type badRequestError struct{ msg string }

func (e *badRequestError) Error() string { return e.msg }

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
