package console_test

import (
	"encoding/json"
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

// The bundle is built before anyone knows which Keycloak it will log in
// against, so the issuer has to reach the browser at runtime. It used to be
// rendered by an entrypoint script running envsubst over a template; now the
// service that holds the configuration answers for it directly.
func TestTheRuntimeEnvironmentIsRenderedFromConfiguration(t *testing.T) {
	handler := console.EnvJS(console.Environment{
		OIDCIssuer:   "http://localhost:8081/realms/cerbos-poc",
		OIDCClientID: "patient-app",
	})

	response := get(handler, "/assets/env.js")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("Content-Type = %q, want javascript, or the browser refuses to execute it", got)
	}
	body := response.Body.String()
	for _, want := range []string{"window.__ENV__", "http://localhost:8081/realms/cerbos-poc", "patient-app"} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q", body, want)
		}
	}
}

// The values are configuration, not code, and they end up inside a script the
// browser executes from the console's own origin. Two things could let one
// escape: a double quote ending the string literal, and the character sequence
// that ends a script element, which the HTML parser looks for before the
// javascript is ever parsed - so quoting alone does not stop it.
func TestTheRuntimeEnvironmentCannotBreakOutOfItsScript(t *testing.T) {
	handler := console.EnvJS(console.Environment{
		OIDCIssuer:   `http://evil/"; window.stolen = document.cookie; //`,
		OIDCClientID: `</script><script>alert(1)</script>`,
	})

	body := get(handler, "/assets/env.js").Body.String()

	// The payload is allowed to appear - it is inert data inside a string
	// literal, and an escaped quote reads as one at a glance. What matters
	// is whether it is still contained, which parsing decides and eyeballing
	// substrings does not.
	if strings.ContainsAny(body, "<>") {
		t.Errorf("a value carried a raw angle bracket into the script element: %s", body)
	}

	// Parsing proves containment: a value that had escaped its literal
	// would leave behind something that is no longer one object. And the
	// escaping has to be reversible, or the console is protected from a
	// value it can then no longer use.
	assigned, ok := strings.CutPrefix(strings.TrimSpace(body), "window.__ENV__ =")
	if !ok {
		t.Fatalf("body = %q, want an assignment to window.__ENV__", body)
	}
	var env struct {
		OIDCIssuer   string `json:"oidcIssuer"`
		OIDCClientID string `json:"oidcClientId"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimSpace(assigned), ";")), &env); err != nil {
		t.Fatalf("the rendered environment is not parseable: %v\n%s", err, body)
	}
	if env.OIDCIssuer != `http://evil/"; window.stolen = document.cookie; //` {
		t.Errorf("issuer round-tripped as %q", env.OIDCIssuer)
	}
	if env.OIDCClientID != `</script><script>alert(1)</script>` {
		t.Errorf("client id round-tripped as %q", env.OIDCClientID)
	}
}

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
