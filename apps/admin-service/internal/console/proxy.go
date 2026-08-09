package console

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// The prefixes the console's browser code calls its own origin on. They are
// constants rather than configuration: the bundle is built with them baked in,
// so a deployment cannot change one half of the agreement.
const (
	// APIPrefix is what the console puts in front of every backend call, so
	// a single-page application's own routes and its API calls cannot
	// collide.
	APIPrefix = "/api"
	// ADSPrefix routes to the ADS.
	ADSPrefix = "/api/ads/"
	// AdminPrefix routes to this service's own administration API.
	AdminPrefix = "/api/admin/"
)

// ADSProxy forwards the console's ADS calls to the ADS (§16.1: the browser
// never reaches it directly).
//
// Resolution happens through the Go resolver, which honours the search domains
// a container's resolv.conf sets. That is the whole reason the deploy overlays
// no longer carry fully-qualified upstream overrides: nginx's resolver never
// applied search-domain expansion, so a bare service name that worked under
// compose was NXDOMAIN against CoreDNS (issue #26).
func ADSProxy(address string) (http.Handler, error) {
	target, err := url.Parse(withScheme(address))
	if err != nil {
		return nil, fmt.Errorf("console: parsing the ADS address %q: %w", address, err)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("console: the ADS address %q names no host", address)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// The ADS is not mounted under the console's prefix and
			// knows nothing about it, so it comes off here. SetURL
			// has already joined the target's own path, so trimming
			// the outbound path is what leaves the ADS's own route.
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, strings.TrimSuffix(ADSPrefix, "/"))
			r.Out.URL.RawPath = ""
			r.SetXForwarded()
		},
	}
	return proxy, nil
}

// StripAPIPrefix removes only the /api the console prefixes its calls with,
// and hands the rest back to the service's own mux.
//
// Only /api comes off. Every administration route is registered with its own
// /admin/... prefix, so a rewrite that took /api/admin off - which is what the
// nginx configuration did, found live in issue #26 - left a path the mux had
// never heard of, and every console call 404'd.
func StripAPIPrefix(next http.Handler) http.Handler {
	return http.StripPrefix(APIPrefix, next)
}

// withScheme lets a plain host:port be configured, which is how every other
// address in this service's environment is written.
func withScheme(address string) string {
	if strings.Contains(address, "://") {
		return address
	}
	return "http://" + address
}
