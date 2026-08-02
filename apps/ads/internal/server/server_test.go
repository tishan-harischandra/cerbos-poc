package server_test

import (
	"encoding/json"
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
