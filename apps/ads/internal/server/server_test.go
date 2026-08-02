package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
