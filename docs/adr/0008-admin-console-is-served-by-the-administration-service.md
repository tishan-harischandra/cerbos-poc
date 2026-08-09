# ADR-008: The Admin Console is served by the Administration Service

The Admin Console ran as its own nginx deployment whose entire behaviour was
"serve `index.html`, strip a path prefix, forward to the ADS or the
Administration Service" — a **shallow module** whose configuration
(`apps/admin-console/nginx.conf.template`) needed more explanation than the
behaviour it encoded, and which produced two production defects found in issue
#26: nginx's resolver does not apply search-domain expansion, so bare compose
service names are NXDOMAIN against CoreDNS, and the `/api/admin/` rewrite
stripped a prefix that every `admin-service` route depends on. We therefore
serve the built Angular bundle from the Administration Service itself, which
becomes the console's BFF in §4.1's sense, and delete the separate deployment.

## Considered options

- **Keep the nginx deployment.** Rejected: the console and the Administration
  Service have the same scaling profile and 1:1 traffic, so the separation
  bought no independent scaling, and the proxy configuration was untestable
  in-process.
- **Move the proxying to an Ingress.** Rejected: it satisfies §16.1 equally well
  but trades an nginx config for an Ingress config without removing the class of
  bug, and makes the console undeployable under docker compose.

## Consequences

- The `/api/ads/` route becomes a Go `httputil.ReverseProxy` inside
  `admin-service`, using the `ADSAddr` it is already configured with. DNS
  resolution moves to the Go resolver, which honours search domains, so the
  `deploy/k8s/overlays/*` upstream patches are deleted rather than fixed.
- `assets/env.js` is rendered by a handler from configuration instead of by the
  `30-render-env-js.sh` entrypoint script.
- The console's route prefixes and the mux that answers them live in one file,
  so a route rename can no longer 404 only in production, and the proxy path is
  reachable from `httptest` for the first time.
- `admin-console` remains an Nx project and a build target; it stops being a
  container image, a Deployment, a Service and a ScaledObject.
- The Administration Service can no longer be replaced by a CDN for static
  assets. Accepted: it is an internal, human-paced surface.
- Because console traffic now drives `admin-service` replica count, the outbox
  publisher's cardinality must stop being a function of that replica count.
  See ADR-009.
