package capability

import (
	"context"
	"fmt"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
)

// ResourceStore is the one read StoreTargetResolver needs: the stored
// instance behind an identifier the browser named.
type ResourceStore interface {
	Resource(ctx context.Context, resourceType, resourceID string) (assignmentstore.Resource, bool, error)
}

// CollectionStatus is the status a collection- or module-scoped target
// carries. Every resource schema requires `status`, and a collection has no
// instance to read one from; ACTIVE is the only defensible value, because the
// locked-record rule guards the instance actions (update, delete, assign) and
// never a list or a module route.
const CollectionStatus = "ACTIVE"

// StoreTargetResolver resolves a symbolic targetRef into a concrete resource
// and its trusted, server-loaded attributes (§12.3 step 2).
//
// It follows DefaultTargetResolver's one convention - an instance-level
// targetRef's concrete ID travels in the routing context, keyed by
// "<targetRef>Id" - and then does the part DefaultTargetResolver could not:
// reads the instance's own attributes from the authorization database rather
// than assuming them. That matters because every resource schema requires
// `status`, so a target resolved without one is refused by schema validation
// and the capability is denied by a mandatory rule no matter what the role
// matrix says.
//
// Nothing here is taken from the browser except which instance is being
// looked at. Tenancy comes from the stored row, not from the caller's token:
// a request naming another tenant's instance must reach the PDP as that other
// tenant so the isolation rule fires, rather than being quietly relabelled
// with the caller's own tenant and allowed.
type StoreTargetResolver struct {
	Store ResourceStore
}

// Resolve implements TargetResolver.
func (r StoreTargetResolver) Resolve(ctx context.Context, query TargetQuery) (ResolvedTarget, error) {
	instanceID, isInstance := query.RouteContext[query.TargetRef+"Id"]
	if !isInstance {
		return ResolvedTarget{
			Resource: capabilityeval.ResourceRef{Kind: query.ResourceKind, ID: query.HospitalID},
			Attributes: map[string]any{
				"tenantId":   query.TenantID,
				"hospitalId": query.HospitalID,
				"status":     CollectionStatus,
			},
		}, nil
	}

	stored, found, err := r.Store.Resource(ctx, query.ResourceKind, instanceID)
	if err != nil {
		return ResolvedTarget{}, fmt.Errorf("resolving %s/%s: %w", query.ResourceKind, instanceID, err)
	}

	// An instance nobody has stored reaches the PDP without a status, which
	// its schema requires, so it is refused there. Inventing one would make
	// an unknown or deleted record indistinguishable from an ordinary one.
	attributes := map[string]any{
		"tenantId":   query.TenantID,
		"hospitalId": query.HospitalID,
	}
	if found {
		attributes["tenantId"] = stored.TenantID
		attributes["hospitalId"] = stored.HospitalID
		attributes["status"] = stored.Status
	}

	return ResolvedTarget{
		Resource:   capabilityeval.ResourceRef{Kind: query.ResourceKind, ID: instanceID},
		Attributes: attributes,
	}, nil
}
