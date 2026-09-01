/**
 * The shape of an application's OIDC client configuration - the same
 * three facts every login and every silent switch needs (issuer, client
 * id, redirect URI). Each application still owns its own runtime
 * defaults (env, ports, ...); this is only the vocabulary the shared web
 * library's functions take as arguments, so an app's own `OidcConfig`
 * token satisfies it structurally with no adapter needed.
 */
export interface OidcClient {
  /** The Keycloak issuer, e.g. http://localhost:8081/realms/tenant-a. */
  issuer: string;
  clientId: string;
  /** Where Keycloak redirects back to after login or a silent switch. */
  redirectUri: string;
}
