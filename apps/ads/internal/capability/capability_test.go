package capability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/capability"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

func permission(resource, action, targetRef string) capabilitycatalog.Expression {
	return capabilitycatalog.Expression{Permission: &capabilitycatalog.PermissionRequirement{
		Resource: resource, Action: action, TargetRef: targetRef,
	}}
}

// identity is the demo doctor, the way the token middleware would have
// left it.
func identity() tokenauth.Identity {
	return tokenauth.Identity{
		PrincipalID: "user-doctor",
		TenantID:    "tenant-a",
		HospitalID:  "hospital-1",
		Roles:       []string{"kc:tenant-a:realm:doctor"},
	}
}

func post(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/capabilities/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity()))
}

// fakeCatalog serves a fixed, in-memory capability catalog for one module.
type fakeCatalog struct {
	revision string
	defs     []capabilitycatalog.UiCapabilityDefinition
}

func (c fakeCatalog) Definitions(context.Context, string) ([]capabilitycatalog.UiCapabilityDefinition, string, error) {
	return c.defs, c.revision, nil
}

// fakeResolver resolves every targetRef to resourceKind/hospitalId, i.e.
// hospital-scoped by default, unless the browser context supplies an
// instance ID keyed by "<targetRef>Id" - the same convention §12.3's own
// example request/definition pair uses (targetRef "patient", context key
// "patientId").
type fakeResolver struct {
	resolved []capability.TargetQuery // records every call, in order
}

func (r *fakeResolver) Resolve(_ context.Context, query capability.TargetQuery) (capability.ResolvedTarget, error) {
	r.resolved = append(r.resolved, query)
	id := query.HospitalID
	if v, ok := query.RouteContext[query.TargetRef+"Id"]; ok {
		id = v
	}
	return capability.ResolvedTarget{
		Resource:   capabilityeval.ResourceRef{Kind: query.ResourceKind, ID: id},
		Attributes: map[string]any{"status": "ACTIVE"},
	}, nil
}

type emptyAssignments struct{}

func (emptyAssignments) For(context.Context, authz.AssignmentQuery) (permissioncontext.Input, error) {
	return permissioncontext.Input{}, nil
}

// recordingPDP answers every leaf as allowed unless told otherwise, and
// counts calls and resources seen across every call so batching and
// chunking behaviour can be asserted on.
type recordingPDP struct {
	calls           int
	resourcesSeen   int
	denied          map[cerbosclient.Leaf]bool
	maxResourcesArg int // largest single-call resource count observed
	lastRequest     cerbosclient.Request
}

func (p *recordingPDP) Check(_ context.Context, req cerbosclient.Request) (cerbosclient.Result, error) {
	p.calls++
	p.resourcesSeen += len(req.Resources)
	p.lastRequest = req
	if len(req.Resources) > p.maxResourcesArg {
		p.maxResourcesArg = len(req.Resources)
	}

	decisions := make(map[cerbosclient.Leaf]cerbosclient.Decision)
	for _, resource := range req.Resources {
		for _, action := range resource.Actions {
			l := cerbosclient.Leaf{Resource: resource.Resource, Action: action}
			decisions[l] = cerbosclient.Decision{Allowed: !p.denied[l]}
		}
	}
	return cerbosclient.Result{CallID: "call-1", Decisions: decisions}, nil
}

func baseConfig(catalog fakeCatalog, pdp *recordingPDP, resolver *fakeResolver) capability.Config {
	return capability.Config{
		PDP:                pdp,
		CapabilityCatalog:  catalog,
		TargetResolver:     resolver,
		Assignments:        emptyAssignments{},
		RootPolicyRevision: "root-v1.4.0",
	}
}

func TestAnUnverifiedRequestIsRefusedWithoutCallingThePDP(t *testing.T) {
	pdp := &recordingPDP{}
	handler := capability.NewHandler(baseConfig(fakeCatalog{}, pdp, &fakeResolver{}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/capabilities/evaluate", strings.NewReader(`{}`)))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if pdp.calls != 0 {
		t.Error("the PDP was called for an unauthenticated request")
	}
}

// §12.3: "The browser must not provide trusted permission decisions,
// tenant ownership or user overrides." A request that tries to name its
// own tenant, hospital or principal is refused outright, just like the
// decision endpoint.
func TestIdentityFieldsInTheRequestBodyAreRefused(t *testing.T) {
	smuggled := map[string]string{
		"a tenant":   `{"tenantId": "tenant-b", "module": "clinical", "capabilityKeys": ["x"]}`,
		"a hospital": `{"hospitalId": "hospital-9", "module": "clinical", "capabilityKeys": ["x"]}`,
	}
	for name, body := range smuggled {
		t.Run(name, func(t *testing.T) {
			pdp := &recordingPDP{}
			handler := capability.NewHandler(baseConfig(fakeCatalog{}, pdp, &fakeResolver{}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, post(t, body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if pdp.calls != 0 {
				t.Error("the PDP was called for a request that named its own identity")
			}
		})
	}
}

func TestAnAllOfCapabilityIsAllowedOnlyWhenEveryLeafIsAllowed(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{Key: "patient.route.edit", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
				permission("patient_record", "read", "patient"),
				permission("patient_record", "update", "patient"),
			}}},
		},
	}
	pdp := &recordingPDP{denied: map[cerbosclient.Leaf]bool{
		{Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"}, Action: "update"}: true,
	}}
	handler := capability.NewHandler(baseConfig(catalog, pdp, &fakeResolver{}))

	body := `{"module":"clinical","capabilityKeys":["patient.route.edit"],"context":{"patientId":"patient-456"}}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(t, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var snapshot capability.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if snapshot.Capabilities["patient.route.edit"].Allowed {
		t.Error("expected patient.route.edit to be denied: update was denied")
	}
}

// targetRef is resolved server-side; browser-supplied "attributes" outside
// the routing-id shape must be refused rather than silently accepted, just
// like the decision endpoint's unknown-field rejection.
func TestUnknownRequestFieldsAreRefused(t *testing.T) {
	pdp := &recordingPDP{}
	handler := capability.NewHandler(baseConfig(fakeCatalog{}, pdp, &fakeResolver{}))

	body := `{"module":"clinical","capabilityKeys":["x"],"attributes":{"status":"ACTIVE"}}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(t, body))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// A leaf shared by several requested capabilities must be checked exactly
// once (issue #11 acceptance criteria), which this asserts indirectly by
// counting the total resources the PDP saw across all calls: two
// capabilities sharing the same resource+action must not double it.
func TestASharedLeafIsCheckedExactlyOnceAcrossCapabilities(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{Key: "patient.route.details", Expression: permission("patient_record", "read", "patient")},
			{Key: "patient.route.edit", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
				permission("patient_record", "read", "patient"),
				permission("patient_record", "update", "patient"),
			}}},
		},
	}
	pdp := &recordingPDP{}
	handler := capability.NewHandler(baseConfig(catalog, pdp, &fakeResolver{}))

	body := `{"module":"clinical","capabilityKeys":["patient.route.details","patient.route.edit"],"context":{"patientId":"patient-456"}}`
	handler.ServeHTTP(httptest.NewRecorder(), post(t, body))

	// One resource (patient_record/patient-456) with two actions
	// (read, update): the PDP must see it as one resource entry, not two.
	if pdp.resourcesSeen != 1 {
		t.Errorf("resources seen by the PDP = %d, want 1 (the shared resource checked once)", pdp.resourcesSeen)
	}
}

// Requesting many capabilities must issue a bounded number of Cerbos
// calls, not one per capability, and calls exceeding the configured
// resource limit must be chunked automatically.
func TestManyCapabilitiesAreBatchedAndChunkedWithinResourceLimits(t *testing.T) {
	const capabilityCount = 250
	var defs []capabilitycatalog.UiCapabilityDefinition
	for i := 0; i < capabilityCount; i++ {
		key := fmt.Sprintf("resource-%d.route.details", i)
		defs = append(defs, capabilitycatalog.UiCapabilityDefinition{
			Key:        key,
			Expression: permission(fmt.Sprintf("resource-%d", i), "read", fmt.Sprintf("target-%d", i)),
		})
	}
	catalog := fakeCatalog{revision: "1", defs: defs}

	keys := make([]string, capabilityCount)
	for i := range keys {
		keys[i] = defs[i].Key
	}
	bodyBytes, err := json.Marshal(capability.Request{Module: "clinical", CapabilityKeys: keys})
	if err != nil {
		t.Fatalf("marshalling request: %v", err)
	}

	pdp := &recordingPDP{}
	cfg := baseConfig(catalog, pdp, &fakeResolver{})
	cfg.MaxResourcesPerCheck = 50
	handler := capability.NewHandler(cfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(t, string(bodyBytes)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	// 250 distinct resources at a limit of 50 per call must take exactly 5
	// calls: bounded, and neither 1 (unbatched) nor 250 (one per capability).
	if pdp.calls != 5 {
		t.Errorf("PDP calls = %d, want 5 (250 resources chunked at 50 per call)", pdp.calls)
	}
	if pdp.maxResourcesArg > 50 {
		t.Errorf("a single Cerbos call carried %d resources, want at most 50", pdp.maxResourcesArg)
	}
	if pdp.resourcesSeen != capabilityCount {
		t.Errorf("total resources seen = %d, want %d", pdp.resourcesSeen, capabilityCount)
	}
}

// The snapshot must carry both revisions and a stable, deterministic
// context fingerprint.
func TestTheSnapshotCarriesBothRevisionsAndAStableFingerprint(t *testing.T) {
	catalog := fakeCatalog{
		revision: "ui-capabilities-v12",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{Key: "patient.route.details", Expression: permission("patient_record", "read", "patient")},
		},
	}
	body := `{"module":"clinical","capabilityKeys":["patient.route.details"],"context":{"patientId":"patient-456"}}`

	fingerprints := make([]string, 2)
	for i := range fingerprints {
		pdp := &recordingPDP{}
		handler := capability.NewHandler(baseConfig(catalog, pdp, &fakeResolver{}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, post(t, body))

		var snapshot capability.Snapshot
		if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
			t.Fatalf("response is not JSON: %v", err)
		}
		if snapshot.CapabilityCatalogRevision != "ui-capabilities-v12" {
			t.Errorf("capabilityCatalogRevision = %q, want %q", snapshot.CapabilityCatalogRevision, "ui-capabilities-v12")
		}
		if snapshot.RootPolicyRevision != "root-v1.4.0" {
			t.Errorf("rootPolicyRevision = %q, want %q", snapshot.RootPolicyRevision, "root-v1.4.0")
		}
		if snapshot.ContextFingerprint == "" {
			t.Error("expected a non-empty contextFingerprint")
		}
		fingerprints[i] = snapshot.ContextFingerprint
	}
	if fingerprints[0] != fingerprints[1] {
		t.Errorf("the same request produced two different fingerprints: %q vs %q", fingerprints[0], fingerprints[1])
	}
}

// End-user responses must carry a stable reason code only; the full
// requirement tree must never appear on that path.
func TestEndUserResponsesCarryNoFailedRequirementsTree(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{Key: "patient.route.edit", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
				permission("patient_record", "read", "patient"),
				permission("patient_record", "update", "patient"),
			}}},
		},
	}
	pdp := &recordingPDP{denied: map[cerbosclient.Leaf]bool{
		{Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "hospital-1"}, Action: "update"}: true,
	}}
	handler := capability.NewHandler(baseConfig(catalog, pdp, &fakeResolver{}))

	body := `{"module":"clinical","capabilityKeys":["patient.route.edit"]}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(t, body))

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	capabilities := raw["capabilities"].(map[string]any)
	result := capabilities["patient.route.edit"].(map[string]any)

	if result["allowed"] != false {
		t.Fatalf("expected patient.route.edit to be denied, got %+v", result)
	}
	if result["reason"] != capabilityeval.ReasonRequiredPermissionDenied {
		t.Errorf("reason = %v, want %q", result["reason"], capabilityeval.ReasonRequiredPermissionDenied)
	}
	if _, present := result["failedRequirements"]; present {
		t.Errorf("end-user response must not carry failedRequirements, got %+v", result["failedRequirements"])
	}
}

// The TargetResolver's own trusted attributes must reach the PDP alongside
// tenantId/hospitalId/permissionContext, not be dropped on the floor.
func TestResolvedTargetAttributesReachThePDP(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{Key: "patient.route.details", Expression: permission("patient_record", "read", "patient")},
		},
	}
	pdp := &recordingPDP{}
	handler := capability.NewHandler(baseConfig(catalog, pdp, &fakeResolver{}))

	body := `{"module":"clinical","capabilityKeys":["patient.route.details"],"context":{"patientId":"patient-456"}}`
	handler.ServeHTTP(httptest.NewRecorder(), post(t, body))

	if len(pdp.lastRequest.Resources) != 1 {
		t.Fatalf("resources sent to the PDP = %d, want 1", len(pdp.lastRequest.Resources))
	}
	if got := pdp.lastRequest.Resources[0].Attr["status"]; got != "ACTIVE" {
		t.Errorf("status attribute = %v, want %q", got, "ACTIVE")
	}
}

// The administration audience may see the complete requirement tree.
func TestAdminAudienceResponsesCarryTheFailedRequirementsTree(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{Key: "patient.route.edit", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
				permission("patient_record", "read", "patient"),
				permission("patient_record", "update", "patient"),
			}}},
		},
	}
	pdp := &recordingPDP{denied: map[cerbosclient.Leaf]bool{
		{Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "hospital-1"}, Action: "update"}: true,
	}}
	cfg := baseConfig(catalog, pdp, &fakeResolver{})
	cfg.Audience = capability.AudienceAdmin
	handler := capability.NewHandler(cfg)

	body := `{"module":"clinical","capabilityKeys":["patient.route.edit"]}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(t, body))

	var snapshot capability.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	result := snapshot.Capabilities["patient.route.edit"]
	if len(result.FailedRequirements) == 0 {
		t.Error("expected the admin audience to carry failed requirement evidence")
	}
}
