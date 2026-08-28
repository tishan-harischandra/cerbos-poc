import { InjectionToken } from '@angular/core';

/**
 * OIDC client configuration for the Business UI's login.
 *
 * The Business UI is the same "browser-facing client" (patient-app) §7.1
 * describes as public and holding no administrative permission of any
 * kind. Its own origin is registered on that client in
 * deploy/keycloak/realm-cerbos-poc.json: a redirect URI Keycloak does not
 * know is refused at the authorization endpoint, before any code is
 * issued.
 */
export interface OidcConfig {
  /** The Keycloak issuer, e.g. http://localhost:8081/realms/cerbos-poc. */
  issuer: string;
  clientId: string;
  /** Where Keycloak redirects back to after login. */
  redirectUri: string;
}

/**
 * Runtime overrides, read from a window global so a deployment can point
 * the UI at a different issuer without rebuilding the bundle. Absent in a
 * local `nx serve` and in unit tests, where the compose-matching defaults
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
