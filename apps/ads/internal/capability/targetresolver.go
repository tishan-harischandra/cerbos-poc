package capability

import (
	"context"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
)

// DefaultTargetResolver resolves a symbolic targetRef into a concrete
// resource using one convention: an instance-level targetRef's concrete ID
// travels in the request's routing context, keyed by "<targetRef>Id" -
// exactly the convention §12.3's own example uses (targetRef "patient",
// context key "patientId"). A targetRef with no matching context entry is
// module- or collection-scoped and resolves to the hospital itself, the
// same tenant/hospital-scoped identity hospital_context and every
// COLLECTION-context leaf already carry in the committed policies
// (deploy/cerbos/policies/resources/hospital_context.yaml).
//
// It loads no resource attributes of its own beyond tenant and hospital:
// this prototype has no FHIR resource data store yet (issue #11 is the
// evaluator and endpoint, not a clinical data layer), so a real
// installation with per-resource attributes (e.g. a patient record's own
// status) replaces this with a resolver that loads them from the
// authoritative service. TargetResolver is a narrow port for exactly that
// reason.
type DefaultTargetResolver struct{}

// Resolve implements TargetResolver.
func (DefaultTargetResolver) Resolve(_ context.Context, query TargetQuery) (ResolvedTarget, error) {
	id := query.HospitalID
	if value, ok := query.RouteContext[query.TargetRef+"Id"]; ok {
		id = value
	}

	return ResolvedTarget{
		Resource: capabilityeval.ResourceRef{Kind: query.ResourceKind, ID: id},
		Attributes: map[string]any{
			"tenantId":   query.TenantID,
			"hospitalId": query.HospitalID,
		},
	}, nil
}
