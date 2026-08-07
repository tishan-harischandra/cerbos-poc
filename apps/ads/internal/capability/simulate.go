package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/canonicalid"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
)

// SimulateRequest is the administration capability simulator's request
// (issue #19, §9.4): the principal is named explicitly, and every
// targetRef's resource attributes are supplied directly as trusted
// sample context rather than resolved from a real resource - the whole
// point of a simulator is trying attributes without a real instance
// existing yet.
type SimulateRequest struct {
	Module           string                    `json:"module"`
	CapabilityKeys   []string                  `json:"capabilityKeys"`
	TenantID         string                    `json:"tenantId"`
	HospitalID       string                    `json:"hospitalId"`
	PrincipalID      string                    `json:"principalId"`
	IdPRoles         []string                  `json:"idpRoles"`
	SampleAttributes map[string]map[string]any `json:"sampleAttributes"`
}

func (r SimulateRequest) validate() error {
	switch {
	case r.Module == "":
		return errors.New("module is required")
	case len(r.CapabilityKeys) == 0:
		return errors.New("capabilityKeys must not be empty")
	case r.TenantID == "":
		return errors.New("tenantId is required")
	case r.HospitalID == "":
		return errors.New("hospitalId is required")
	case r.PrincipalID == "":
		return errors.New("principalId is required")
	}
	return nil
}

// LeafDecision is one permission leaf's decision in the full requirement
// tree the simulator exposes to administrators only (§12.4, issue #19).
type LeafDecision struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Target   string `json:"target"`
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason"`
}

// SimulateSnapshot is the capability simulator's response: the same
// Snapshot shape the runtime endpoint serves, plus the complete,
// per-leaf requirement tree - administration-audience evidence that must
// never reach an end-user path (§12.4).
type SimulateSnapshot struct {
	Snapshot
	RequirementTree []LeafDecision `json:"requirementTree"`
}

// sampleTargetResolver satisfies TargetResolver from the request's own
// SampleAttributes, so the simulator reuses evaluate() - the exact same
// target-resolution, leaf-flattening, Cerbos-batching and
// capabilityeval.Evaluate composition the runtime snapshot endpoint uses
// - rather than a second evaluation implementation (§21). Only the
// source of a targetRef's attributes differs; every step after that is
// identical code.
type sampleTargetResolver struct {
	sampleAttributes map[string]map[string]any
}

func (s sampleTargetResolver) Resolve(_ context.Context, query TargetQuery) (ResolvedTarget, error) {
	return ResolvedTarget{
		Resource:   capabilityeval.ResourceRef{Kind: query.ResourceKind, ID: "sample:" + query.TargetRef},
		Attributes: s.sampleAttributes[query.TargetRef],
	}, nil
}

// NewSimulateHandler builds the POST /internal/capabilities/simulate
// handler (issue #19). It is reachable only from other backend services
// over the internal compose network, never from a browser - the same
// trust boundary every other /internal/* route on this service relies
// on - and it always answers at AudienceAdmin, since existing is the
// whole point.
func NewSimulateHandler(cfg Config) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	maxResources := cfg.MaxResourcesPerCheck
	if maxResources <= 0 {
		maxResources = DefaultMaxResourcesPerCheck
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := tokenauth.From(r.Context()); !ok {
			logger.ErrorContext(r.Context(), "the capability simulate endpoint was reached without a verified identity")
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

		if role, found := simulatedReservedRole(req.IdPRoles); found {
			logger.ErrorContext(r.Context(), "a capability simulation named a reserved role",
				slog.String("role", role))
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("role %q is reserved for the platform", role))
			return
		}

		identity := tokenauth.Identity{
			PrincipalID: req.PrincipalID,
			TenantID:    req.TenantID,
			HospitalID:  req.HospitalID,
			Roles:       req.IdPRoles,
		}

		simCfg := cfg
		simCfg.TargetResolver = sampleTargetResolver{sampleAttributes: req.SampleAttributes}
		simCfg.Audience = AudienceAdmin

		snapshot, leafOutcomes, err := evaluate(r.Context(), simCfg, maxResources, identity, Request{
			Module:         req.Module,
			CapabilityKeys: req.CapabilityKeys,
		})
		if err != nil {
			var badRequest *badRequestError
			if errors.As(err, &badRequest) {
				writeError(w, http.StatusBadRequest, badRequest.Error())
				return
			}
			logger.ErrorContext(r.Context(), "simulating capabilities failed",
				slog.String("principalId", req.PrincipalID),
				slog.String("module", req.Module),
				slog.Any("error", err))
			writeError(w, http.StatusServiceUnavailable, "could not simulate capabilities")
			return
		}

		tree := make([]LeafDecision, 0, len(leafOutcomes))
		for leaf, outcome := range leafOutcomes {
			tree = append(tree, LeafDecision{
				Resource: leaf.Resource.Kind, Action: leaf.Action, Target: leaf.Resource.ID,
				Allowed: outcome.Allowed, Reason: outcome.Reason,
			})
		}
		sort.Slice(tree, func(i, j int) bool {
			if tree[i].Resource != tree[j].Resource {
				return tree[i].Resource < tree[j].Resource
			}
			if tree[i].Target != tree[j].Target {
				return tree[i].Target < tree[j].Target
			}
			return tree[i].Action < tree[j].Action
		})

		writeJSON(w, http.StatusOK, SimulateSnapshot{Snapshot: snapshot, RequirementTree: tree})
	})
}

func simulatedReservedRole(roles []string) (string, bool) {
	for _, role := range roles {
		if canonicalid.IsReserved(role) {
			return role, true
		}
	}
	return "", false
}
