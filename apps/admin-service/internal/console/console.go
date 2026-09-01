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
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

// Config describes the console this service serves.
type Config struct {
	// Dir is the built Angular bundle on disk.
	Dir string
	// ADSAddr is where the console's ADS calls are forwarded. It is the
	// same address this service already uses for the simulator, so the
	// browser's route to the ADS and the service's own cannot diverge.
	ADSAddr string
	// HostResolver resolves which tenant's issuer and client id the
	// runtime environment names, from the request's own Host header
	// (issue #83) - never a value baked into the bundle at build time.
	HostResolver tenantregistry.HostResolver
}

// EnvJSPath is where index.html expects the runtime environment.
const EnvJSPath = "/assets/env.js"

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
