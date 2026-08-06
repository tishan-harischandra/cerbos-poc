package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/server"
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

func TestReadyzIsUnavailableWhileADependencyIsUnreachable(t *testing.T) {
	handler := server.New(server.Config{
		Dependencies: []server.Dependency{{
			Name:  "cerbos",
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
	if got := body.Dependencies["cerbos"]; got == "ok" || got == "" {
		t.Errorf("GET /readyz cerbos dependency = %q, want a failure reason", got)
	}
}

func TestTheDecisionEndpointIsMountedWhenAHandlerIsConfigured(t *testing.T) {
	handler := server.New(server.Config{
		AuthzHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/authz/check", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("POST /internal/authz/check status = %d, want the configured handler to answer", rec.Code)
	}
}

// Without a decision handler the route must not exist at all. A stub answering
// 200 would be indistinguishable from a real allow.
func TestTheDecisionEndpointIsAbsentWhenNoHandlerIsConfigured(t *testing.T) {
	handler := server.New(server.Config{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/authz/check", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /internal/authz/check status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTheCapabilityEndpointIsMountedWhenAHandlerIsConfigured(t *testing.T) {
	handler := server.New(server.Config{
		CapabilityHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/capabilities/evaluate", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("POST /internal/capabilities/evaluate status = %d, want the configured handler to answer", rec.Code)
	}
}

// Without a capability handler the route must not exist at all, for the
// same reason the decision endpoint's absence is 404 rather than a stub.
func TestTheCapabilityEndpointIsAbsentWhenNoHandlerIsConfigured(t *testing.T) {
	handler := server.New(server.Config{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/capabilities/evaluate", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /internal/capabilities/evaluate status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTheMetricsEndpointIsMountedWhenAHandlerIsConfigured(t *testing.T) {
	handler := server.New(server.Config{
		MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("GET /metrics status = %d, want the configured handler to answer", rec.Code)
	}
}

func TestTheMetricsEndpointIsAbsentWhenNoHandlerIsConfigured(t *testing.T) {
	handler := server.New(server.Config{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /metrics status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestReadyzGivesUpOnADependencyThatNeverAnswers(t *testing.T) {
	handler := server.New(server.Config{
		ReadinessTimeout: 50 * time.Millisecond,
		Dependencies: []server.Dependency{{
			Name: "cerbos",
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
			{Name: "cerbos", Probe: func(context.Context) error { return nil }},
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
	for _, name := range []string{"cerbos", "postgres"} {
		if body.Dependencies[name] != "ok" {
			t.Errorf("GET /readyz %s dependency = %q, want %q", name, body.Dependencies[name], "ok")
		}
	}
}
