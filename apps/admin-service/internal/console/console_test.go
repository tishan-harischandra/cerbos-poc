package console_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/console"
)

// The console is a single-page application: the browser asks for
// /user-overrides on a refresh or a deep link, the server has no such file,
// and the router inside index.html is what knows the route. Returning 404
// there would make every bookmark and every refresh a broken page.
func TestAnUnknownPathIsAnsweredByTheApplicationShell(t *testing.T) {
	dir := bundle(t, map[string]string{
		"index.html":     "<!doctype html><title>console</title>",
		"main-abc123.js": "console.log('bundle')",
	})
	handler := assets(t, dir)

	response := get(handler, "/user-overrides/tenant-a")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 so a deep link reaches the router", response.Code)
	}
	if !strings.Contains(response.Body.String(), "<title>console</title>") {
		t.Errorf("body = %q, want the application shell", response.Body.String())
	}
}

func TestABuiltAssetIsServedAsItself(t *testing.T) {
	dir := bundle(t, map[string]string{
		"index.html":     "<!doctype html>",
		"main-abc123.js": "console.log('bundle')",
	})
	handler := assets(t, dir)

	response := get(handler, "/main-abc123.js")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if body := response.Body.String(); !strings.Contains(body, "console.log('bundle')") {
		t.Errorf("body = %q, want the asset itself rather than the shell", body)
	}
}

// A missing asset must not be answered with the shell. A bundle whose
// javascript 404s is obvious; one that silently returns HTML with a 200
// produces a blank page and a syntax error in the console instead.
func TestAMissingAssetIsNotAnsweredByTheShell(t *testing.T) {
	dir := bundle(t, map[string]string{"index.html": "<!doctype html>"})
	handler := assets(t, dir)

	for _, path := range []string{"/main-deleted.js", "/assets/logo.svg", "/styles.css"} {
		response := get(handler, path)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 rather than the shell", path, response.Code)
		}
	}
}

// Rendering the runtime environment itself - the JSON shape, the
// script-injection escaping - is libs/tenantregistry's own contract now
// (issue #83's EnvJSHandler, tested there): this package only wires that
// handler's resolver into its own mux, which
// TestTheRuntimeEnvironmentIsServedToTheBrowser (apps/admin-service/internal/
// server) already covers end to end.

func bundle(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	return dir
}

func assets(t *testing.T, dir string) http.Handler {
	t.Helper()
	handler, err := console.Assets(dir)
	if err != nil {
		t.Fatalf("console.Assets: %v", err)
	}
	return handler
}

func get(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
