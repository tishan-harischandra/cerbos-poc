// Package console serves the Admin Console's built Angular bundle from the
// Administration Service (ADR-008).
//
// The console used to be its own nginx deployment whose entire behaviour was
// "serve index.html, strip a prefix, forward". That configuration needed more
// explanation than the behaviour it encoded, and it produced two defects that
// no test could reach, because nginx configuration is not reachable from a
// test: bare service names were NXDOMAIN against CoreDNS, and a rewrite
// stripped a prefix every admin route depends on.
//
// Everything here is deliberately reachable from httptest.
package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Environment is the configuration the browser needs at runtime.
//
// The bundle is built long before anyone knows which Keycloak an installation
// logs in against, and a static build has no templating step of its own, so
// these values reach it as a small script the service renders.
type Environment struct {
	OIDCIssuer   string
	OIDCClientID string
}

// EnvJSPath is where index.html expects the runtime environment.
const EnvJSPath = "/assets/env.js"

// EnvJS renders the runtime environment as the script index.html loads.
//
// The values are marshalled as JSON rather than interpolated. They are
// configuration arriving from the environment, and they end up inside a script
// this origin serves: a stray quote would close the string literal early, and
// an operator's typo would become script injection on the console's own
// origin. json.Marshal escapes both the quote and the `<` that would otherwise
// close the surrounding script tag.
func EnvJS(env Environment) http.Handler {
	// A JSON object literal is also a JavaScript object literal, so the
	// whole value is marshalled in one step rather than interpolated field
	// by field. json.Marshal already escapes the quote; escapeForScript
	// handles the one thing JSON encoding does not know about.
	marshalled, _ := json.Marshal(struct {
		OIDCIssuer   string `json:"oidcIssuer"`
		OIDCClientID string `json:"oidcClientId"`
	}{OIDCIssuer: env.OIDCIssuer, OIDCClientID: env.OIDCClientID})
	body := fmt.Sprintf("window.__ENV__ = %s;\n", escapeForScript(marshalled))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		// The issuer can change without the bundle changing, so this one
		// file must not be cached the way the fingerprinted assets are.
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(body))
	})
}

// escapeForScript hides the character sequences that would end the script
// element early. Inside a <script>, the parser looks for "</script" and
// "<!--" before the javascript is ever parsed, so a value containing one would
// terminate the script no matter how well quoted it is as a JSON string.
func escapeForScript(marshalled []byte) string {
	escaped := string(marshalled)
	escaped = strings.ReplaceAll(escaped, "<", `\u003c`)
	escaped = strings.ReplaceAll(escaped, ">", `\u003e`)
	escaped = strings.ReplaceAll(escaped, "&", `\u0026`)
	return escaped
}

// Assets serves the built bundle, falling back to index.html so a deep link
// or a refresh reaches the application's own router.
//
// The fallback applies only where the browser is asking for a page. A request
// for a missing script gets a 404: answering it with HTML and a 200 turns a
// missing asset into a blank page and a syntax error, which is far harder to
// diagnose than the 404 it really is.
func Assets(dir string) (http.Handler, error) {
	if dir == "" {
		return nil, errors.New("console: no bundle directory configured")
	}
	shell := filepath.Join(dir, "index.html")
	if _, err := os.Stat(shell); err != nil {
		return nil, fmt.Errorf("console: reading the application shell: %w", err)
	}

	root := os.DirFS(dir)
	files := http.FileServerFS(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			http.ServeFile(w, r, shell)
			return
		}

		if _, err := fs.Stat(root, name); err == nil {
			files.ServeHTTP(w, r)
			return
		}

		// Nothing on disk. A path that looks like a file is a missing
		// asset; anything else is a route the router inside the shell
		// knows about.
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, shell)
	}), nil
}
