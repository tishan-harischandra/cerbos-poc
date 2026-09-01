package tenantregistry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// EnvJSHandler renders the runtime environment a browser-facing bundle
// reads at startup (ADR-008), resolving which tenant's issuer and client
// id to render from the request's own Host header rather than a value
// baked in at build time (issue #83). Both front ends serve this from the
// same origin their bundle is served from, so the request's Host is
// exactly the tenant subdomain the browser is on.
//
// An unknown host is a clear, distinguishable error - not a JavaScript
// blob a browser would try to execute as one tenant's configuration when
// it names none, and not a redirect toward a realm this installation does
// not serve.
func EnvJSHandler(resolver HostResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry, err := resolver.Resolve(r.Host)
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "no tenant is registered for this host: %v\n", err)
			return
		}

		// A JSON object literal is also a JavaScript object literal, so
		// the whole value is marshalled in one step rather than
		// interpolated field by field. json.Marshal already escapes the
		// quote; escapeForScript handles the one thing JSON encoding does
		// not know about.
		marshalled, _ := json.Marshal(struct {
			OIDCIssuer   string `json:"oidcIssuer"`
			OIDCClientID string `json:"oidcClientId"`
		}{OIDCIssuer: entry.Issuer, OIDCClientID: entry.BrowserClientID})

		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		// The tenant a host resolves to never changes without a
		// deployment change, but the value is derived per request rather
		// than baked into the bundle, so this one file must not be cached
		// the way the fingerprinted assets are.
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "window.__ENV__ = %s;\n", escapeForScript(marshalled))
	})
}

// escapeForScript hides the character sequences that would end the script
// element early. Inside a <script>, the parser looks for "</script" and
// "<!--" before the javascript is ever parsed, so a value containing one
// would terminate the script no matter how well quoted it is as a JSON
// string.
func escapeForScript(marshalled []byte) string {
	escaped := string(marshalled)
	escaped = strings.ReplaceAll(escaped, "<", `\u003c`)
	escaped = strings.ReplaceAll(escaped, ">", `\u003e`)
	escaped = strings.ReplaceAll(escaped, "&", `\u0026`)
	return escaped
}
