package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// evaluate implements the §12.3 backend evaluation algorithm end to end.
// The returned leafOutcomes carries every leaf's decision, allowed and
// denied alike - NewHandler's own snapshot only surfaces the denied ones
// as FailedRequirements, but the simulator (issue #19) needs the complete
// requirement tree, and reusing this return value rather than
// re-evaluating is what keeps it from being a second implementation.
func evaluate(ctx context.Context, cfg Config, maxResources int, identity tokenauth.Identity, req Request) (Snapshot, map[capabilityeval.Leaf]capabilityeval.LeafOutcome, error) {
	// Step 1: load definitions for the active capability-catalog revision.
	allDefs, catalogRevision, err := cfg.CapabilityCatalog.Definitions(ctx, req.Module)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("loading capability definitions: %w", err)
	}

	defs, err := selectRequested(allDefs, req.CapabilityKeys)
	if err != nil {
		return Snapshot{}, nil, err
	}

	// Step 2: resolve every targetRef into a concrete resource, server-side.
	targets, targetOrder, resourceAttrs, err := resolveTargets(ctx, cfg, identity, req, defs)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("resolving targets: %w", err)
	}

	// Steps 3-4: flatten and deduplicate every permission leaf.
	leaves, err := capabilityeval.Flatten(defs, targets)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("flattening capabilities: %w", err)
	}

	// Group deduplicated leaves by resource, so assignments and Cerbos are
	// each asked about one resource once, regardless of how many
	// capabilities or actions touch it (§12.3 steps 4-5).
	resourceActions, resourceOrder := groupLeavesByResource(leaves)

	// Step 5: resolve assignments once per resource for this subject.
	revision, resourceChecks, err := buildResourceChecks(ctx, cfg, identity, resourceOrder, resourceActions, resourceAttrs)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("resolving assignments: %w", err)
	}

	// Steps 6-7: issue a bounded number of Cerbos calls and merge results.
	decisions, firedRules, err := checkInChunks(ctx, cfg, maxResources, identity, resourceChecks)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("checking resources: %w", err)
	}

	leafOutcomes := make(map[capabilityeval.Leaf]capabilityeval.LeafOutcome, len(leaves))
	for _, leaf := range leaves {
		ref := cerbosclient.ResourceRef{Kind: leaf.Resource.Kind, ID: leaf.Resource.ID}
		decision := decisions[cerbosclient.Leaf{Resource: ref, Action: leaf.Action}]
		leafOutcomes[leaf] = capabilityeval.LeafOutcome{
			Allowed: decision.Allowed,
			Reason:  string(authz.DecisionSource(leaf.Action, decision.Allowed, firedRules[ref])),
		}
	}

	// Step 8: evaluate every capability expression in memory.
	outcomes, err := capabilityeval.Evaluate(defs, targets, leafOutcomes)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("evaluating capabilities: %w", err)
	}

	// Step 9: assemble the snapshot.
	capabilities := make(map[string]CapabilityResult, len(outcomes))
	for key, outcome := range outcomes {
		result := CapabilityResult{Allowed: outcome.Allowed, Reason: outcome.Reason}
		// §12.4: end-user responses carry a stable reason code only. The
		// full requirement tree is administration-audience evidence and
		// must never reach an end-user path (issue #11 acceptance
		// criteria), so it is only attached for AudienceAdmin.
		if cfg.Audience == AudienceAdmin {
			for _, f := range outcome.FailedRequirements {
				result.FailedRequirements = append(result.FailedRequirements, FailedRequirement{
					Resource: f.Resource, Action: f.Action, Target: f.Target, Reason: f.Reason,
				})
			}
		}
		capabilities[key] = result
	}

	return Snapshot{
		AuthorizationRevision:     revision,
		RootPolicyRevision:        cfg.RootPolicyRevision,
		CapabilityCatalogRevision: catalogRevision,
		TenantID:                  identity.TenantID,
		HospitalID:                identity.HospitalID,
		Module:                    req.Module,
		ContextFingerprint:        contextFingerprint(identity, req, targetOrder, targets),
		Capabilities:              capabilities,
	}, leafOutcomes, nil
}

// selectRequested filters defs down to exactly the requested capability
// keys, in request order, failing with a client error if a key names a
// capability the module does not have.
func selectRequested(defs []capabilitycatalog.UiCapabilityDefinition, keys []string) ([]capabilitycatalog.UiCapabilityDefinition, error) {
	byKey := make(map[string]capabilitycatalog.UiCapabilityDefinition, len(defs))
	for _, d := range defs {
		byKey[d.Key] = d
	}

	selected := make([]capabilitycatalog.UiCapabilityDefinition, 0, len(keys))
	for _, key := range keys {
		def, ok := byKey[key]
		if !ok {
			return nil, &badRequestError{msg: fmt.Sprintf("unknown capability key %q", key)}
		}
		selected = append(selected, def)
	}
	return selected, nil
}

// resolveTargets resolves every distinct targetRef referenced by defs into
// a concrete resource, server-side, exactly once per targetRef (§12.3 step
// 2). targetOrder is returned purely for a deterministic fingerprint.
// resourceAttrs carries each resolved resource's trusted, server-loaded
// attributes, keyed by the same ResourceRef the leaves and Cerbos batch
// will use, so a resource resolved through two different targetRefs still
// contributes its attributes once.
func resolveTargets(ctx context.Context, cfg Config, identity tokenauth.Identity, req Request, defs []capabilitycatalog.UiCapabilityDefinition) (map[string]capabilityeval.ResourceRef, []string, map[capabilityeval.ResourceRef]map[string]any, error) {
	kindByTargetRef := make(map[string]string)
	collectTargetRefs(defs, kindByTargetRef)

	targetRefs := make([]string, 0, len(kindByTargetRef))
	for ref := range kindByTargetRef {
		targetRefs = append(targetRefs, ref)
	}
	sort.Strings(targetRefs)

	targets := make(map[string]capabilityeval.ResourceRef, len(targetRefs))
	resourceAttrs := make(map[capabilityeval.ResourceRef]map[string]any, len(targetRefs))
	for _, targetRef := range targetRefs {
		resolved, err := cfg.TargetResolver.Resolve(ctx, TargetQuery{
			TenantID:     identity.TenantID,
			HospitalID:   identity.HospitalID,
			ResourceKind: kindByTargetRef[targetRef],
			TargetRef:    targetRef,
			RouteContext: req.Context,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("resolving targetRef %q: %w", targetRef, err)
		}
		targets[targetRef] = resolved.Resource
		if resolved.Attributes != nil {
			resourceAttrs[resolved.Resource] = resolved.Attributes
		}
	}
	return targets, targetRefs, resourceAttrs, nil
}

func collectTargetRefs(defs []capabilitycatalog.UiCapabilityDefinition, out map[string]string) {
	var walk func(e capabilitycatalog.Expression)
	walk = func(e capabilitycatalog.Expression) {
		switch {
		case e.Permission != nil:
			out[e.Permission.TargetRef] = e.Permission.Resource
		case e.AllOf != nil:
			for _, c := range e.AllOf {
				walk(c)
			}
		case e.AnyOf != nil:
			for _, c := range e.AnyOf {
				walk(c)
			}
		}
	}
	for _, def := range defs {
		walk(def.Expression)
	}
}

// groupLeavesByResource collapses the deduplicated leaf list into one
// action set per resource, so a resource with several required actions is
// still asked about once (§12.3 step 6). resourceOrder is sorted so the
// resulting Cerbos batches - and therefore which chunk a resource lands in
// - are deterministic.
func groupLeavesByResource(leaves []capabilityeval.Leaf) (map[capabilityeval.ResourceRef][]string, []capabilityeval.ResourceRef) {
	actionsByResource := make(map[capabilityeval.ResourceRef][]string)
	for _, leaf := range leaves {
		actionsByResource[leaf.Resource] = append(actionsByResource[leaf.Resource], leaf.Action)
	}

	order := make([]capabilityeval.ResourceRef, 0, len(actionsByResource))
	for ref := range actionsByResource {
		order = append(order, ref)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].Kind != order[j].Kind {
			return order[i].Kind < order[j].Kind
		}
		return order[i].ID < order[j].ID
	})
	return actionsByResource, order
}

// buildResourceChecks resolves assignments once per resource for this
// principal (§12.3 step 5) and assembles each resource's permissionContext,
// returning the authorization revision they were resolved at.
func buildResourceChecks(ctx context.Context, cfg Config, identity tokenauth.Identity, order []capabilityeval.ResourceRef, actionsByResource map[capabilityeval.ResourceRef][]string, resourceAttrs map[capabilityeval.ResourceRef]map[string]any) (int64, []cerbosclient.ResourceCheck, error) {
	var revision int64
	checks := make([]cerbosclient.ResourceCheck, 0, len(order))

	for _, ref := range order {
		input, err := cfg.Assignments.For(ctx, authz.AssignmentQuery{
			TenantID:     identity.TenantID,
			HospitalID:   identity.HospitalID,
			PrincipalID:  identity.PrincipalID,
			ResourceKind: ref.Kind,
			ResourceID:   ref.ID,
			IdPRoles:     identity.Roles,
		})
		if err != nil {
			return 0, nil, fmt.Errorf("resolving assignments for %s/%s: %w", ref.Kind, ref.ID, err)
		}

		assembled := permissioncontext.Assemble(input)
		if assembled.PermissionRevision > revision {
			revision = assembled.PermissionRevision
		}

		// The resolver's own trusted attributes come first so
		// tenantId/hospitalId/permissionContext, which every resource must
		// carry regardless of what the resolver returned, can never be
		// shadowed by it.
		attr := make(map[string]any, len(resourceAttrs[ref])+3)
		for name, value := range resourceAttrs[ref] {
			attr[name] = value
		}
		attr["tenantId"] = identity.TenantID
		attr["hospitalId"] = identity.HospitalID
		attr["permissionContext"] = assembled.AsMap()

		checks = append(checks, cerbosclient.ResourceCheck{
			Resource: cerbosclient.ResourceRef{Kind: ref.Kind, ID: ref.ID},
			Attr:     attr,
			Actions:  actionsByResource[ref],
		})
	}

	return revision, checks, nil
}

// checkInChunks issues a bounded number of Cerbos CheckResources calls -
// never one per capability, and automatically split if the resource count
// would exceed maxResources - and merges every chunk's decisions and fired
// rules (§12.3 steps 6-7, §15.2).
func checkInChunks(ctx context.Context, cfg Config, maxResources int, identity tokenauth.Identity, checks []cerbosclient.ResourceCheck) (map[cerbosclient.Leaf]cerbosclient.Decision, map[cerbosclient.ResourceRef][]string, error) {
	decisions := make(map[cerbosclient.Leaf]cerbosclient.Decision)
	firedRules := make(map[cerbosclient.ResourceRef][]string)

	if len(checks) == 0 {
		return decisions, firedRules, nil
	}

	for start := 0; start < len(checks); start += maxResources {
		end := start + maxResources
		if end > len(checks) {
			end = len(checks)
		}

		result, err := cfg.PDP.Check(ctx, cerbosclient.Request{
			Principal: cerbosclient.Principal{
				ID: identity.PrincipalID,
				Attr: map[string]any{
					"tenantId":   identity.TenantID,
					"hospitalId": identity.HospitalID,
					"idpRoles":   identity.Roles,
				},
			},
			Resources: checks[start:end],
		})
		if err != nil {
			return nil, nil, err
		}
		for leaf, decision := range result.Decisions {
			decisions[leaf] = decision
		}
		for ref, rules := range result.FiredRules {
			firedRules[ref] = rules
		}
	}

	return decisions, firedRules, nil
}

// contextFingerprint hashes everything the resolved snapshot depends on
// beyond the two revisions: the module, the exact capability keys asked
// for, and the routing context that drove targetRef resolution. Two
// requests that resolve to the same fingerprint are asking the identical
// question, which is what makes it safe for the frontend to cache a
// snapshot by fingerprint (§12.6).
func contextFingerprint(identity tokenauth.Identity, req Request, targetOrder []string, targets map[string]capabilityeval.ResourceRef) string {
	var b strings.Builder
	b.WriteString(identity.TenantID)
	b.WriteString("|")
	b.WriteString(identity.HospitalID)
	b.WriteString("|")
	b.WriteString(req.Module)

	keys := append([]string(nil), req.CapabilityKeys...)
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString("|k:")
		b.WriteString(key)
	}

	for _, targetRef := range targetOrder {
		ref := targets[targetRef]
		b.WriteString("|t:")
		b.WriteString(targetRef)
		b.WriteString("=")
		b.WriteString(ref.Kind)
		b.WriteString(":")
		b.WriteString(ref.ID)
	}

	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// formatCatalogRevision renders an int64 catalog revision into the string
// shape §12.4 shows ("ui-capabilities-v12"). Exported for adapters that
// store the revision as a bare number.
func formatCatalogRevision(revision int64) string {
	return "ui-capabilities-v" + strconv.FormatInt(revision, 10)
}
