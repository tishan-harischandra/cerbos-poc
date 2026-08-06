// Rendered into assets/env.js by docker-entrypoint.d/30-render-env-js.sh at
// container start, the same envsubst mechanism the nginx image already uses
// for nginx.conf.template. A static Angular build has no server-side
// templating step of its own, so this is how a compose-time value (which
// Keycloak issuer to log in against) reaches a bundle that was already
// built before anyone knew the answer.
window.__ENV__ = {
  oidcIssuer: '${OIDC_ISSUER}',
  oidcClientId: '${OIDC_CLIENT_ID}',
};
