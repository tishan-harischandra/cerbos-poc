import { InjectionToken } from '@angular/core';

/**
 * OIDC client configuration for the Admin Console's login (§7.1, §9.1).
 *
 * The console is the "browser-facing client" (patient-app) §7.1 describes
 * as public and holding no administrative permission of any kind - the
 * same client every other browser-facing surface on this port uses.
 */
export interface OidcConfig {
  /** The Keycloak issuer, e.g. http://localhost:8081/realms/cerbos-poc. */
  issuer: string;
  clientId: string;
  /** Where Keycloak redirects back to after login. */
  redirectUri: string;
}

/**
 * Runtime overrides rendered by docker-entrypoint.d/30-render-env-js.sh from
 * OIDC_ISSUER and OIDC_CLIENT_ID (see assets/env.template.js). Absent in a
 * local `nx serve` or in unit tests, where the compose-matching defaults
 * below apply instead.
 */
interface RuntimeEnv {
  oidcIssuer?: string;
  oidcClientId?: string;
}

function runtimeEnv(): RuntimeEnv {
  return (window as unknown as { __ENV__?: RuntimeEnv }).__ENV__ ?? {};
}

export const OIDC_CONFIG = new InjectionToken<OidcConfig>('OIDC_CONFIG', {
  providedIn: 'root',
  factory: () => ({
    issuer: runtimeEnv().oidcIssuer || 'http://localhost:8081/realms/cerbos-poc',
    clientId: runtimeEnv().oidcClientId || 'patient-app',
    redirectUri: `${window.location.origin}/callback`,
  }),
});
