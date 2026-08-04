package authz_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

const validRequest = `{
  "tenantId": "tenant-a",
  "hospitalId": "hospital-1",
  "principalId": "user-123",
  "idpRoles": ["kc:realm:patient-app:doctor"],
  "resources": [
    {
      "kind": "patient_record",
      "id": "patient-456",
      "attributes": {"tenantId": "tenant-a", "hospitalId": "hospital-1", "status": "ACTIVE"},
      "actions": ["read", "update"]
    }
  ]
}`

// The synthetic role is the ADS' to inject, never the caller's to claim. A token
// presenting one must be refused before any decision is attempted (§21, ADR-003).
func TestARequestPresentingTheSyntheticRoleIsRefusedWithoutCallingThePDP(t *testing.T) {
	pdp := &recordingPDP{}
	handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	body := strings.Replace(validRequest,
		`"idpRoles": ["kc:realm:patient-app:doctor"]`,
		`"idpRoles": ["kc:realm:patient-app:doctor", "sys:permission-evaluator"]`, 1)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post(body))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if pdp.calls != 0 {
		t.Errorf("the PDP was called %d times for a rejected token, want 0", pdp.calls)
	}
}

func TestAnyReservedRolePrefixIsRefused(t *testing.T) {
	for _, role := range []string{"sys:permission-evaluator", "sys:anything", "SYS:UPPERCASE"} {
		t.Run(role, func(t *testing.T) {
			pdp := &recordingPDP{}
			handler := authz.NewHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

			body := strings.Replace(validRequest,
				`"idpRoles": ["kc:realm:patient-app:doctor"]`,
				`"idpRoles": ["`+role+`"]`, 1)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, post(body))

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
	if len(roles) != 1 || roles[0] != "kc:realm:patient-app:doctor" {
		t.Errorf("idpRoles = %v, want [kc:realm:patient-app:doctor]", roles)
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

func TestMalformedRequestsAreRejected(t *testing.T) {
	cases := map[string]string{
		"not JSON at all":       `{`,
		"no tenantId":           strings.Replace(validRequest, `"tenantId": "tenant-a",`, "", 1),
		"no hospitalId":         strings.Replace(validRequest, `"hospitalId": "hospital-1",`, "", 1),
		"no principalId":        strings.Replace(validRequest, `"principalId": "user-123",`, "", 1),
		"no resources":          `{"tenantId":"tenant-a","hospitalId":"hospital-1","principalId":"user-123","resources":[]}`,
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

func post(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/authz/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
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
