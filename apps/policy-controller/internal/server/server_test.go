package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/policy-controller/internal/server"
)

func TestNew_ReadyzReportsLeaderAndLastResult(t *testing.T) {
	status := server.NewStatus()
	status.SetLeader(true)
	status.SetLastResult("root-v1.4.0", true, "")

	handler := server.New(status)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, "root-v1.4.0") {
		t.Fatalf("body = %q, want it to mention the last revision", body)
	}
}

func TestNew_ReadyzReportsFailureOfLastRun(t *testing.T) {
	status := server.NewStatus()
	status.SetLeader(true)
	status.SetLastResult("root-v1.4.0", false, "cerbos compile failed")

	handler := server.New(status)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (readiness reflects the HTTP surface, not the last release outcome)", rec.Code)
	}
	if !contains(rec.Body.String(), "cerbos compile failed") {
		t.Fatalf("body = %q, want it to mention the last error", rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
