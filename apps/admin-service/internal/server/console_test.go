package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/catalogapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/console"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/server"
)

// One mux answers both audiences (ADR-008), so the cases that matter are the
// ones about them coexisting: the console must not shadow the API, and the
// API must not shadow the console.

// The regression issue #26 found live, now reachable from a test. Every
// administration route is registered under /admin, and the console calls them
// under /api/admin. Taking both prefixes off - which the nginx rewrite did -
// left a path the mux had never heard of, and every console call 404'd.
func TestTheConsoleReachesTheAdministrationAPIThroughItsOwnOrigin(t *testing.T) {
	handler := consoleHandler(t, "http://ads.invalid")

	// An unauthenticated call is still routed: 401 proves the route
	// matched and the token check ran, which is exactly what a 404 would
	// not prove.
	response := get(handler, "/api/admin/authz/resources")

	if response.Code == http.StatusNotFound {
		t.Fatal("the console's admin call 404'd: the /admin prefix every route is registered under did not survive the rewrite")
	}
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 from the token check on a route that matched", response.Code)
	}
}

// The API keeps its own unprefixed routes for everything that is not a
// browser: probes, and any caller talking to the service directly.
func TestTheServiceKeepsAnsweringItsOwnRoutes(t *testing.T) {
	handler := consoleHandler(t, "http://ads.invalid")

	for path, want := range map[string]int{
		"/healthz":               http.StatusOK,
		"/admin/authz/resources": http.StatusUnauthorized,
	} {
		if got := get(handler, path).Code; got != want {
			t.Errorf("GET %s = %d, want %d - serving the console must not move the API", path, got, want)
		}
	}
}

// A path the mux does not know is a console route, not a 404: the router
// inside the bundle decides. But that fallback must not swallow the API's own
// namespace, or a mistyped endpoint would answer 200 with HTML and a caller
// would parse the login page as JSON.
func TestTheConsoleFallbackDoesNotSwallowTheAPI(t *testing.T) {
	handler := consoleHandler(t, "http://ads.invalid")

	if got := get(handler, "/user-overrides/tenant-a").Code; got != http.StatusOK {
		t.Errorf("a console deep link = %d, want 200 from the application shell", got)
	}
	if got := get(handler, "/admin/authz/no-such-endpoint").Code; got == http.StatusOK {
		t.Error("an unknown administration endpoint was answered by the console shell")
	}
}

// §16.1: the browser reaches the ADS only through this origin. The proxy is
// registered even though the ADS is unreachable here - what matters is that
// the route exists and forwards, which a 502 proves and a 404 disproves.
func TestTheADSIsReachedThroughThisOriginRatherThanDirectly(t *testing.T) {
	handler := consoleHandler(t, "http://127.0.0.1:1")

	response := get(handler, "/api/ads/internal/directory/users/user-1/roles")

	if response.Code == http.StatusNotFound {
		t.Fatal("the console's ADS route is not registered, so the browser would have to reach the ADS directly")
	}
	if response.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 from a proxy that tried and could not reach the ADS", response.Code)
	}
}

func TestTheRuntimeEnvironmentIsServedToTheBrowser(t *testing.T) {
	handler := consoleHandler(t, "http://ads.invalid")

	response := get(handler, console.EnvJSPath)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if body := response.Body.String(); !strings.Contains(body, "tenant-a") {
		t.Errorf("body = %q, want the configured issuer", body)
	}
}

// A bundle directory that does not exist is a deployment mistake, and the
// service says so at startup rather than serving 404s to every browser.
func TestAMissingBundleIsRefusedAtStartup(t *testing.T) {
	_, err := server.New(server.Config{
		Console: &console.Config{Dir: filepath.Join(t.TempDir(), "never-built"), ADSAddr: "http://ads.invalid"},
	})
	if err == nil {
		t.Error("New accepted a console whose bundle is not there")
	}
}

func consoleHandler(t *testing.T, adsAddr string) http.Handler {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>console</title>"), 0o644); err != nil {
		t.Fatalf("writing the application shell: %v", err)
	}

	return newHandler(t, server.Config{
		// A verifier is what registers the administration routes at
		// all; with none, they would be absent and the cases below
		// would pass for the wrong reason.
		Verifier: fakeVerifier{},
		Catalog:  &catalogapi.Handler{},
		Console: &console.Config{
			Dir:     dir,
			ADSAddr: adsAddr,
			Environment: console.Environment{
				OIDCIssuer:   "http://localhost:8081/realms/tenant-a",
				OIDCClientID: "patient-app",
			},
		},
	})
}

func get(handler http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}
