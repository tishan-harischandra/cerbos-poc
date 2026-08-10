package capability_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/capability"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// fakeResourceStore answers Resource lookups from a fixed map.
type fakeResourceStore struct {
	resources map[string]assignmentstore.Resource
	err       error
	queried   []string
}

func (s *fakeResourceStore) Resource(_ context.Context, resourceType, resourceID string) (assignmentstore.Resource, bool, error) {
	if s.err != nil {
		return assignmentstore.Resource{}, false, s.err
	}
	key := resourceType + "/" + resourceID
	s.queried = append(s.queried, key)
	resource, found := s.resources[key]
	return resource, found, nil
}

func storedPatient() assignmentstore.Resource {
	return assignmentstore.Resource{
		ResourceType: "patient_record", ResourceID: "patient-456",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE",
		UpdatedAt: time.Now(),
	}
}

// The defect this resolver exists to fix: every resource schema requires
// `status`, so a target resolved without one is refused by the PDP's schema
// validation and the capability is denied by a mandatory rule - whatever the
// role matrix says. A snapshot endpoint that can only ever answer "denied" is
// worse than none.
func TestAnInstanceTargetCarriesTheStoredResourceStatus(t *testing.T) {
	store := &fakeResourceStore{resources: map[string]assignmentstore.Resource{
		"patient_record/patient-456": storedPatient(),
	}}
	resolver := capability.StoreTargetResolver{Store: store}

	resolved, err := resolver.Resolve(context.Background(), capability.TargetQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1",
		ResourceKind: "patient_record", TargetRef: "patient",
		RouteContext: map[string]string{"patientId": "patient-456"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.Resource.ID != "patient-456" {
		t.Errorf("resolved id = %q, want the instance the browser named", resolved.Resource.ID)
	}
	if resolved.Attributes["status"] != "ACTIVE" {
		t.Errorf("status = %v, want the stored ACTIVE", resolved.Attributes["status"])
	}
}

// The stored row is the authority on tenancy, not the token: an instance
// belonging to another tenant must reach the PDP as that other tenant, so the
// isolation rule fires instead of being quietly satisfied by the caller's own
// identity.
func TestAnInstanceTargetCarriesTheStoredTenancyRatherThanTheCallers(t *testing.T) {
	other := storedPatient()
	other.TenantID = "tenant-b"
	other.HospitalID = "hospital-9"
	store := &fakeResourceStore{resources: map[string]assignmentstore.Resource{
		"patient_record/patient-456": other,
	}}
	resolver := capability.StoreTargetResolver{Store: store}

	resolved, err := resolver.Resolve(context.Background(), capability.TargetQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1",
		ResourceKind: "patient_record", TargetRef: "patient",
		RouteContext: map[string]string{"patientId": "patient-456"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.Attributes["tenantId"] != "tenant-b" || resolved.Attributes["hospitalId"] != "hospital-9" {
		t.Errorf("attributes = %v, want the stored tenant-b/hospital-9", resolved.Attributes)
	}
}

// A named instance nobody has ever stored cannot be shown to be safe, so it
// must reach the PDP without a status and be refused there. Inventing one
// would make a deleted or unknown record look ordinary.
func TestANamedInstanceWithNoStoredRowCarriesNoStatus(t *testing.T) {
	store := &fakeResourceStore{resources: map[string]assignmentstore.Resource{}}
	resolver := capability.StoreTargetResolver{Store: store}

	resolved, err := resolver.Resolve(context.Background(), capability.TargetQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1",
		ResourceKind: "patient_record", TargetRef: "patient",
		RouteContext: map[string]string{"patientId": "patient-999"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, present := resolved.Attributes["status"]; present {
		t.Errorf("attributes = %v, want no status for an unknown instance", resolved.Attributes)
	}
}

// A collection- or module-scoped targetRef names no instance at all: it
// resolves to the hospital itself, and there is no row to read. ACTIVE is the
// only defensible status for it - a collection cannot be locked, and the
// locked-record rule only guards the instance actions - and without one every
// COLLECTION-context capability would fail schema validation forever.
func TestACollectionTargetResolvesToTheHospitalAndIsActive(t *testing.T) {
	store := &fakeResourceStore{resources: map[string]assignmentstore.Resource{}}
	resolver := capability.StoreTargetResolver{Store: store}

	resolved, err := resolver.Resolve(context.Background(), capability.TargetQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1",
		ResourceKind: "patient_record", TargetRef: "patientCollection",
		RouteContext: map[string]string{"patientId": "patient-456"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.Resource.ID != "hospital-1" {
		t.Errorf("resolved id = %q, want the hospital", resolved.Resource.ID)
	}
	if resolved.Attributes["status"] != "ACTIVE" {
		t.Errorf("status = %v, want ACTIVE for a collection scope", resolved.Attributes["status"])
	}
	if len(store.queried) != 0 {
		t.Errorf("the store was queried for a collection scope: %v", store.queried)
	}
}

// A store that cannot answer must not be reported as "no such resource":
// fail-closed is right, but silently so would make an outage look like a
// permission change.
func TestAStoreFailureIsReportedRatherThanTreatedAsAMissingResource(t *testing.T) {
	store := &fakeResourceStore{err: errors.New("connection refused")}
	resolver := capability.StoreTargetResolver{Store: store}

	_, err := resolver.Resolve(context.Background(), capability.TargetQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1",
		ResourceKind: "patient_record", TargetRef: "patient",
		RouteContext: map[string]string{"patientId": "patient-456"},
	})
	if err == nil {
		t.Fatal("a store failure resolved successfully")
	}
}
