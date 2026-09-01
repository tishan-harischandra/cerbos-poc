package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

func TestHealthzReportsTheServiceIsAlive(t *testing.T) {
	handler := server.New(server.Config{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("GET /healthz body is not JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("GET /healthz status field = %q, want %q", body.Status, "ok")
	}
}

// business-ui's runtime environment (issue #83) is absent unless this
// deployment is actually configured to serve it - a bare API deployment
// must not gain a new route it never asked for.
func TestEnvJSIsAbsentWithNoHostResolverConfigured(t *testing.T) {
	handler := server.New(server.Config{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, server.EnvJSPath, nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET %s status = %d, want 404 with no host resolver configured", server.EnvJSPath, rec.Code)
	}
}

func TestEnvJSRendersTheRequestingHostsOwnTenant(t *testing.T) {
	resolver := tenantregistry.NewHostResolver([]tenantregistry.Entry{
		{Realm: "tenant-a", Issuer: "http://localhost:8081/realms/tenant-a", BrowserClientID: "patient-app"},
	})
	handler := server.New(server.Config{HostResolver: resolver})

	req := httptest.NewRequest(http.MethodGet, server.EnvJSPath, nil)
	req.Host = "tenant-a.example.test"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", server.EnvJSPath, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "tenant-a") {
		t.Errorf("body = %q, want the requesting host's own tenant", rec.Body.String())
	}
}

func TestReadyzIsUnavailableWhileADependencyIsUnreachable(t *testing.T) {
	handler := server.New(server.Config{
		Dependencies: []server.Dependency{{
			Name:  "ads",
			Probe: func(context.Context) error { return errors.New("connection refused") },
		}},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body struct {
		Status       string            `json:"status"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("GET /readyz body is not JSON: %v", err)
	}
	if body.Status != "unavailable" {
		t.Errorf("GET /readyz status field = %q, want %q", body.Status, "unavailable")
	}
	if got := body.Dependencies["ads"]; got == "ok" || got == "" {
		t.Errorf("GET /readyz ads dependency = %q, want a failure reason", got)
	}
}

func TestTheFHIRHandlerIsMountedWhenConfigured(t *testing.T) {
	handler := server.New(server.Config{
		FHIRHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fhir/patient_record/patient-1", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("GET /fhir/... status = %d, want the configured handler to answer", rec.Code)
	}
}

// Without a FHIR handler the route must not exist at all. A stub answering
// 200 would be indistinguishable from a real decision.
func TestTheFHIRHandlerIsAbsentWhenNoneIsConfigured(t *testing.T) {
	handler := server.New(server.Config{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fhir/patient_record/patient-1", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /fhir/... status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestReadyzGivesUpOnADependencyThatNeverAnswers(t *testing.T) {
	handler := server.New(server.Config{
		ReadinessTimeout: 50 * time.Millisecond,
		Dependencies: []server.Dependency{{
			Name: "ads",
			Probe: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}},
	})

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GET /readyz never returned; a hung dependency must not hang readiness")
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadyzIsReadyOnceEveryDependencyAnswers(t *testing.T) {
	handler := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "ads", Probe: func(context.Context) error { return nil }},
			{Name: "postgres", Probe: func(context.Context) error { return nil }},
		},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Status       string            `json:"status"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("GET /readyz body is not JSON: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("GET /readyz status field = %q, want %q", body.Status, "ready")
	}
	for _, name := range []string{"ads", "postgres"} {
		if body.Dependencies[name] != "ok" {
			t.Errorf("GET /readyz %s dependency = %q, want %q", name, body.Dependencies[name], "ok")
		}
	}
}
