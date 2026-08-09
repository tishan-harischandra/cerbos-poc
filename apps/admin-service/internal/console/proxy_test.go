package console_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/console"
)

// §16.1: the browser never reaches the ADS directly. The console asks its own
// origin, and the Administration Service forwards - which is also what keeps
// the decision path off any public network.
func TestTheADSProxyForwardsWithoutItsOwnPrefix(t *testing.T) {
	var forwarded string
	ads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.URL.Path
		if r.URL.RawQuery != "" {
			forwarded += "?" + r.URL.RawQuery
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer ads.Close()

	proxy, err := console.ADSProxy(ads.URL)
	if err != nil {
		t.Fatalf("console.ADSProxy: %v", err)
	}

	response := get(proxy, "/api/ads/internal/directory/users/user-1/roles?tenant=tenant-a")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want the ADS's own answer", response.Code)
	}
	// The ADS knows nothing about the console's routing, so its own prefix
	// has to come off before the request arrives.
	if want := "/internal/directory/users/user-1/roles?tenant=tenant-a"; forwarded != want {
		t.Errorf("the ADS received %q, want %q", forwarded, want)
	}
}

// The regression from issue #26, in the form that could not be written before:
// nginx's rewrite stripped `/api/admin/` entirely, but every route this
// service registers carries its own `/admin/` prefix, so every console call
// 404'd against the mux. Only `/api` may come off.
func TestTheAdminPrefixSurvivesTheRewrite(t *testing.T) {
	var reached string
	admin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
	})

	console.StripAPIPrefix(admin).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/admin/authz/resources", nil))

	if want := "/admin/authz/resources"; reached != want {
		t.Errorf("the mux was asked for %q, want %q - the route prefix every admin handler is registered under", reached, want)
	}
}

// A proxy that could not reach the ADS must say so as a gateway failure. The
// default ReverseProxy behaviour is a 502 with the error on the server's log
// and nothing useful in the body, which is right - what matters is that it is
// not mistaken for the ADS having answered.
func TestAnUnreachableADSIsAGatewayFailure(t *testing.T) {
	proxy, err := console.ADSProxy("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("console.ADSProxy: %v", err)
	}

	response := get(proxy, "/api/ads/internal/directory/users/user-1/roles")

	if response.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when the ADS cannot be reached", response.Code)
	}
}

func TestAnUnparseableADSAddressIsRefused(t *testing.T) {
	if _, err := console.ADSProxy("://not a url"); err == nil {
		t.Error("ADSProxy accepted an address it could never dial")
	}
}
