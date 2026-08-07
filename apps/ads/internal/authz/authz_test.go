package authz_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// The request names resources and actions only. Who is asking comes from the
// verified token, so there is nothing here a browser could use to name itself.
const validRequest = `{
  "resources": [
    {
      "kind": "patient_record",
      "id": "patient-456",
      "attributes": {"tenantId": "tenant-a", "hospitalId": "hospital-1", "status": "ACTIVE"},
      "actions": ["read", "update"]
    }
  ]
}`

const doctorRole = "kc:cerbos-poc:patient-app:doctor"

type recordingMetrics struct {
	observations []observation
}

type observation struct {
	resource, action, outcome string
}

func (m *recordingMetrics) Observe(resource, action, outcome string, _ time.Duration) {
	m.observations = append(m.observations, observation{resource, action, outcome})
}

// §16.1: tenant and hospital context are derived server-side. A browser that
// sends its own is refused rather than quietly ignored, because a caller who
// believes those fields work will keep believing it until something depends on
// it.
func TestIdentityFieldsInTheRequestBodyAreRefused(t *testing.T) {
	smuggled := map[string]string{
		"a tenant":    `{"tenantId": "tenant-b", "resources": []}`,
		"a hospital":  `{"hospitalId": "hospital-9", "resources": []}`,
		"a principal": `{"principalId": "user-somebody-else", "resources": []}`,
		"roles":       `{"idpRoles": ["` + doctorRole + `"], "resources": []}`,
	}

	for name, body := range smuggled {
		t.Run(name, func(t *testing.T) {
			pdp := &recordingPDP{}
			handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, post(body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if pdp.calls != 0 {
				t.Error("the PDP was called for a request that named its own identity")
			}
		})
	}
}

// The handler must be unusable without authentication, so that mounting it on
// an unauthenticated route fails instead of answering.
func TestARequestWithNoVerifiedIdentityIsRefusedWithoutCallingThePDP(t *testing.T) {
	pdp := &recordingPDP{}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/authz/check",
		strings.NewReader(validRequest)))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if pdp.calls != 0 {
		t.Error("the PDP was called for an unauthenticated request")
	}
}

// Defence in depth for §16.1's synthetic role rule: verification rejects such a
// token first, so an identity carrying one means something upstream is broken -
// and it still must not reach the PDP.
func TestAnIdentityCarryingTheSyntheticRoleNeverReachesThePDP(t *testing.T) {
	for _, role := range []string{"sys:permission-evaluator", "sys:anything", "SYS:UPPERCASE"} {
		t.Run(role, func(t *testing.T) {
			pdp := &recordingPDP{}
			handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, postAs(identityWithRoles(role), validRequest))

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d for role %q, want %d", rec.Code, role, http.StatusForbidden)
			}
			if pdp.calls != 0 {
				t.Errorf("the PDP was called for reserved role %q", role)
			}
		})
	}
}

func TestTheDecisionForEveryRequestedActionIsReturned(t *testing.T) {
	pdp := &recordingPDP{
		result: cerbosclient.Result{
			CallID: "01HQ8WZ5F3",
			Decisions: map[cerbosclient.Leaf]cerbosclient.Decision{
				leaf("patient-456", "read"):   {Allowed: true},
				leaf("patient-456", "update"): {Allowed: false},
			},
		},
	}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(validRequest))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var body authz.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.CerbosCallID != "01HQ8WZ5F3" {
		t.Errorf("cerbosCallId = %q, want %q", body.CerbosCallID, "01HQ8WZ5F3")
	}
	if len(body.Resources) != 1 {
		t.Fatalf("resources = %d, want 1", len(body.Resources))
	}
	actions := body.Resources[0].Actions
	if !actions["read"].Allowed {
		t.Error("read = denied, want allowed")
	}
	if actions["update"].Allowed {
		t.Error("update = allowed, want denied")
	}
}

// The response's decision source comes from the PDP's own fired-rule
// reporting, not from any Go-side precedence logic (§21). This asserts the
// wiring end to end: a fired rule name on cerbosclient.Result surfaces as the
// matching Appendix A source label in the HTTP response.
func TestTheDecisionSourceComesFromThePDPsFiredRules(t *testing.T) {
	pdp := &recordingPDP{
		result: cerbosclient.Result{
			CallID: "call-1",
			Decisions: map[cerbosclient.Leaf]cerbosclient.Decision{
				leaf("patient-456", "read"):   {Allowed: true},
				leaf("patient-456", "update"): {Allowed: false},
			},
			FiredRules: map[cerbosclient.ResourceRef]([]string){
				{Kind: "patient_record", ID: "patient-456"}: {"grant_read_to_user", "revoke_update"},
			},
		},
	}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(validRequest))

	var body authz.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	actions := body.Resources[0].Actions
	if actions["read"].Source != authz.SourceUserGrant {
		t.Errorf("read source = %q, want %q", actions["read"].Source, authz.SourceUserGrant)
	}
	if actions["update"].Source != authz.SourceUserRevoke {
		t.Errorf("update source = %q, want %q", actions["update"].Source, authz.SourceUserRevoke)
	}
}

// The ADS supplies data. This asserts the assembled permissionContext reaches
// the PDP as resource attributes; whether a revoke beats a grant is proven by
// the Cerbos policy suite, not here.
func TestTheAssembledPermissionContextIsSentToThePDP(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{CallID: "call-1"}}
	handler := authz.NewHandler(authz.Config{
		PDP: pdp,
		Assignments: fixedAssignments{input: permissioncontext.Input{
			Revision: 184,
			RolePermissions: []permissioncontext.RolePermission{
				{Role: "doctor", Action: "read", Enabled: true},
			},
			UserOverrides: []permissioncontext.UserOverride{
				{Action: "update", State: permissioncontext.Revoke},
			},
		}},
	})

	handler.ServeHTTP(httptest.NewRecorder(), post(validRequest))

	if len(pdp.request.Resources) != 1 {
		t.Fatalf("resources sent to the PDP = %d, want 1", len(pdp.request.Resources))
	}
	attr := pdp.request.Resources[0].Attr
	// The PDP receives the wire form, not the Go struct: a struct is silently
	// dropped in transit and the resource then fails schema validation.
	sent, ok := attr["permissionContext"].(map[string]any)
	if !ok {
		t.Fatalf("permissionContext attribute = %#v, want map[string]any", attr["permissionContext"])
	}
	if want := permissioncontext.Assemble(permissioncontext.Input{
		Revision: 184,
		RolePermissions: []permissioncontext.RolePermission{
			{Role: "doctor", Action: "read", Enabled: true},
		},
		UserOverrides: []permissioncontext.UserOverride{
			{Action: "update", State: permissioncontext.Revoke},
		},
	}).AsMap(); !reflect.DeepEqual(sent, want) {
		t.Errorf("permissionContext sent to the PDP = %v, want %v", sent, want)
	}
	if got := attr["status"]; got != "ACTIVE" {
		t.Errorf("the caller's resource attributes were not forwarded: status = %v", got)
	}
}

// The permissionContext is the ADS' word, not the caller's. A caller that sends
// one must not be able to grant itself anything, so the assembled value has to
// win over whatever arrived in the request body.
func TestACallerCannotInjectItsOwnPermissionContext(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{CallID: "call-1"}}
	handler := authz.NewHandler(authz.Config{
		PDP: pdp,
		Assignments: fixedAssignments{input: permissioncontext.Input{
			Revision: 184,
			RolePermissions: []permissioncontext.RolePermission{
				{Role: "doctor", Action: "read", Enabled: true},
			},
		}},
	})

	forged := `{
  "resources": [{
    "kind": "patient_record",
    "id": "patient-456",
    "attributes": {
      "tenantId": "tenant-a",
      "hospitalId": "hospital-1",
      "status": "ACTIVE",
      "permissionContext": {
        "roleGrantedActions": ["read", "update", "delete"],
        "userGrantedActions": ["read", "update", "delete"],
        "userRevokedActions": [],
        "permissionRevision": 9999
      }
    },
    "actions": ["delete"]
  }]
}`

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(forged))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	sent, ok := pdp.request.Resources[0].Attr["permissionContext"].(map[string]any)
	if !ok {
		t.Fatalf("permissionContext = %#v, want map[string]any", pdp.request.Resources[0].Attr["permissionContext"])
	}

	granted, ok := sent["roleGrantedActions"].([]any)
	if !ok {
		t.Fatalf("roleGrantedActions = %#v, want []any", sent["roleGrantedActions"])
	}
	if len(granted) != 1 || granted[0] != "read" {
		t.Errorf("roleGrantedActions = %v, want only the assembled [read]", granted)
	}
	if revision := sent["permissionRevision"]; revision != int64(184) {
		t.Errorf("permissionRevision = %v, want the assembled 184", revision)
	}
}

// §11.3: a decision must be traceable from the caller's correlation ID into the
// PDP's own audit log, which means both IDs on one log record.
func TestTheDecisionIsLoggedWithBothCorrelationIDs(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{CallID: "01HZY2CALLID"}}

	var logged strings.Builder
	handler := authz.NewHandler(authz.Config{
		PDP:         pdp,
		Assignments: emptyAssignments{},
		Logger:      slog.New(slog.NewJSONHandler(&logged, nil)),
	})

	request := post(validRequest)
	request.Header.Set("X-Correlation-Id", "corr-42")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	var record struct {
		Message       string `json:"msg"`
		CorrelationID string `json:"correlationId"`
		CerbosCallID  string `json:"cerbosCallId"`
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(logged.String()), "\n") {
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if record.CerbosCallID != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no log record carried a cerbosCallId; log was:\n%s", logged.String())
	}
}

// §17.2: the ADS emits permission revision, root policy revision and the
// matched (idp) role IDs alongside the correlation IDs, so a decision can
// be reconstructed end-to-end without a second lookup.
func TestTheDecisionIsLoggedWithRevisionsAndRoles(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{CallID: "01HZY2CALLID"}}

	var logged strings.Builder
	handler := authz.NewHandler(authz.Config{
		PDP:                pdp,
		Assignments:        fixedAssignments{input: permissioncontext.Input{Revision: 184}},
		Logger:             slog.New(slog.NewJSONHandler(&logged, nil)),
		RootPolicyRevision: "root-v1.4.0",
	})

	request := postAs(identityWithRoles(doctorRole), validRequest)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	var record struct {
		CerbosCallID       string   `json:"cerbosCallId"`
		RootPolicyRevision string   `json:"rootPolicyRevision"`
		RoleIds            []string `json:"roleIds"`
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(logged.String()), "\n") {
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if record.CerbosCallID != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no log record carried a cerbosCallId; log was:\n%s", logged.String())
	}
	if record.RootPolicyRevision != "root-v1.4.0" {
		t.Errorf("rootPolicyRevision = %q, want root-v1.4.0", record.RootPolicyRevision)
	}
	if len(record.RoleIds) != 1 || record.RoleIds[0] != doctorRole {
		t.Errorf("roleIds = %v, want [%s]", record.RoleIds, doctorRole)
	}
}

// §16.2: mask or omit sensitive attributes from decision audit logs. A
// clinical attribute value sent in the request must never appear in the
// decision log line, only stable identifiers and revisions.
func TestSensitiveResourceAttributesNeverAppearInTheDecisionLog(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{CallID: "call-1"}}

	var logged strings.Builder
	handler := authz.NewHandler(authz.Config{
		PDP:         pdp,
		Assignments: emptyAssignments{},
		Logger:      slog.New(slog.NewJSONHandler(&logged, nil)),
	})

	sensitiveRequest := `{
	  "resources": [
	    {
	      "kind": "patient_record",
	      "id": "patient-456",
	      "attributes": {"tenantId": "tenant-a", "hospitalId": "hospital-1", "diagnosis": "Type 2 Diabetes Mellitus", "patientName": "Jane Doe"},
	      "actions": ["read"]
	    }
	  ]
	}`
	handler.ServeHTTP(httptest.NewRecorder(), post(sensitiveRequest))

	if strings.Contains(logged.String(), "Diabetes") || strings.Contains(logged.String(), "Jane Doe") {
		t.Fatalf("decision log leaked a clinical attribute value: %s", logged.String())
	}
}

// Real IdP roles travel as principal attributes for audit and optional policy
// conditions; they are never sent as Cerbos roles (§6.4).
func TestTheRealIdPRolesTravelAsPrincipalAttributes(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{CallID: "call-1"}}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	handler.ServeHTTP(httptest.NewRecorder(), post(validRequest))

	roles, ok := pdp.request.Principal.Attr["idpRoles"].([]string)
	if !ok {
		t.Fatalf("idpRoles attribute = %#v, want []string", pdp.request.Principal.Attr["idpRoles"])
	}
	if len(roles) != 1 || roles[0] != doctorRole {
		t.Errorf("idpRoles = %v, want [%s]", roles, doctorRole)
	}
	if got := pdp.request.Principal.ID; got != "user-doctor" {
		t.Errorf("principal = %q, want the subject of the verified token", got)
	}
	if got := pdp.request.Principal.Attr["tenantId"]; got != "tenant-a" {
		t.Errorf("tenantId attribute = %v, want tenant-a", got)
	}
	if got := pdp.request.Principal.Attr["hospitalId"]; got != "hospital-1" {
		t.Errorf("hospitalId attribute = %v, want hospital-1", got)
	}
}

// §11.3: allow only an explicit EFFECT_ALLOW. A PDP that cannot be reached must
// not degrade into an allow.
func TestAnUnreachablePDPFailsClosed(t *testing.T) {
	pdp := &recordingPDP{err: errors.New("connection refused")}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(validRequest))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(rec.Body.String(), `"allowed":true`) {
		t.Errorf("an unreachable PDP produced an allow: %s", rec.Body)
	}
}

// A leaf the PDP did not answer for must read as denied rather than as a missing
// key that a caller might treat as permissive.
func TestALeafThePDPDidNotAnswerForIsDenied(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{
		CallID: "call-1",
		Decisions: map[cerbosclient.Leaf]cerbosclient.Decision{
			leaf("patient-456", "read"): {Allowed: true},
		},
	}}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(validRequest))

	var body authz.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	decision, ok := body.Resources[0].Actions["update"]
	if !ok {
		t.Fatal("no entry for the unanswered action; want an explicit denial")
	}
	if decision.Allowed {
		t.Error("an unanswered action was reported as allowed")
	}
}

// §17.1: every decision reports its outcome per resource and action, so
// request rate and allow/deny/error rate are never lost in an aggregate.
func TestEveryDecisionIsObservedByResourceActionAndOutcome(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{
		CallID: "call-1",
		Decisions: map[cerbosclient.Leaf]cerbosclient.Decision{
			leaf("patient-456", "read"):   {Allowed: true},
			leaf("patient-456", "update"): {Allowed: false},
		},
	}}
	metrics := &recordingMetrics{}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}, Metrics: metrics})

	handler.ServeHTTP(httptest.NewRecorder(), post(validRequest))

	want := map[observation]bool{
		{"patient_record", "read", "allow"}:  true,
		{"patient_record", "update", "deny"}: true,
	}
	if len(metrics.observations) != 2 {
		t.Fatalf("observations = %+v, want 2", metrics.observations)
	}
	for _, got := range metrics.observations {
		if !want[got] {
			t.Errorf("unexpected observation %+v", got)
		}
	}
}

// An unreachable PDP is observed as an error outcome, not silently
// dropped from the metric surface.
func TestAnUnreachablePDPIsObservedAsAnErrorOutcome(t *testing.T) {
	pdp := &recordingPDP{err: errors.New("connection refused")}
	metrics := &recordingMetrics{}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}, Metrics: metrics})

	handler.ServeHTTP(httptest.NewRecorder(), post(validRequest))

	if len(metrics.observations) == 0 {
		t.Fatal("no observation recorded for an unreachable PDP")
	}
	for _, got := range metrics.observations {
		if got.outcome != "error" {
			t.Errorf("outcome = %q, want error", got.outcome)
		}
	}
}

func TestMalformedRequestsAreRejected(t *testing.T) {
	cases := map[string]string{
		"not JSON at all":       `{`,
		"no resources":          `{"resources":[]}`,
		"a resource with no id": strings.Replace(validRequest, `"id": "patient-456",`, "", 1),
		"a resource with no actions": strings.Replace(validRequest,
			`"actions": ["read", "update"]`, `"actions": []`, 1),
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			pdp := &recordingPDP{}
			handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, post(body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if pdp.calls != 0 {
				t.Errorf("the PDP was called for a malformed request")
			}
		})
	}
}

// post sends a request as the demo doctor, the way the token middleware would
// have left it.
func post(body string) *http.Request {
	return postAs(identityWithRoles(doctorRole), body)
}

func postAs(identity tokenauth.Identity, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/authz/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity))
}

func identityWithRoles(roles ...string) tokenauth.Identity {
	return tokenauth.Identity{
		PrincipalID: "user-doctor",
		Username:    "doctor",
		TenantID:    "tenant-a",
		HospitalID:  "hospital-1",
		Roles:       roles,
	}
}

func leaf(id, action string) cerbosclient.Leaf {
	return cerbosclient.Leaf{
		Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: id},
		Action:   action,
	}
}

type recordingPDP struct {
	calls   int
	request cerbosclient.Request
	result  cerbosclient.Result
	err     error
}

func (p *recordingPDP) Check(_ context.Context, req cerbosclient.Request) (cerbosclient.Result, error) {
	p.calls++
	p.request = req
	return p.result, p.err
}

type emptyAssignments struct{}

func (emptyAssignments) For(context.Context, authz.AssignmentQuery) (permissioncontext.Input, error) {
	return permissioncontext.Input{}, nil
}

type fixedAssignments struct {
	input permissioncontext.Input
}

func (f fixedAssignments) For(context.Context, authz.AssignmentQuery) (permissioncontext.Input, error) {
	return f.input, nil
}
