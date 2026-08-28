/**
 * PKCE (RFC 7636) helpers for the Business UI's OIDC login.
 *
 * The Business UI is the same public browser-facing client the Admin
 * Console uses (§7.1: "Public, so it holds no secret"): with no client
 * secret to protect the authorization code exchange, PKCE stands in for
 * one. Everything here is pure and testable without a network call or a
 * real browser redirect.
 *
 * This duplicates apps/admin-console/src/app/auth/pkce.ts. Two copies of
 * an authorization flow is a defect in its own right; the intended
 * resolution is to lift the whole auth directory into a shared
 * libs/web/auth alongside libs/web/capability, which is tracked
 * separately rather than done here.
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
