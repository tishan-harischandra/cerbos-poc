/**
 * PKCE (RFC 7636) helpers for an OIDC Authorization Code login against
 * Keycloak.
 *
 * Both the Admin Console and the Business UI are the same public
 * browser-facing client (§7.1: "Public, so it holds no secret"): with no
 * client secret to protect the authorization code exchange, PKCE stands in
 * for one. Previously duplicated per application; issue #84's hospital
 * switcher needed a second PKCE exchange for the silent re-authentication
 * flow, which is what finally forced the move here rather than a third
 * copy. Everything here is pure and testable without a network call or a
 * real browser redirect.
 */

const CODE_VERIFIER_LENGTH = 64;
const UNRESERVED_CHARS =
  'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~';

/** Generates a cryptographically random RFC 7636 code_verifier. */
export function generateCodeVerifier(): string {
  const bytes = new Uint8Array(CODE_VERIFIER_LENGTH);
  crypto.getRandomValues(bytes);
  let verifier = '';
  for (const byte of bytes) {
    verifier += UNRESERVED_CHARS[byte % UNRESERVED_CHARS.length];
  }
  return verifier;
}

/** Generates an opaque random state value for CSRF protection. */
export function generateState(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return base64UrlEncode(bytes);
}

/**
 * Derives the S256 code_challenge from a code_verifier:
 * base64url(sha256(verifier)), per RFC 7636 §4.2.
 */
export async function deriveCodeChallenge(verifier: string): Promise<string> {
  const encoded = new TextEncoder().encode(verifier);
  const digest = await crypto.subtle.digest('SHA-256', encoded);
  return base64UrlEncode(new Uint8Array(digest));
}

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
